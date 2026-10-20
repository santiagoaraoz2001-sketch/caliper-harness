package memory

import (
	"context"
	"fmt"
	"sort"
	"specific-harness/internal/prompts"
	"strings"
	"unicode"
	"unicode/utf8"

	"specific-harness/internal/store"
)

// stopwords are dropped from recall matching. Fixed set: matching
// vocabulary is algorithmic, not a tunable (the length floor is YAML).
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "for": true, "from": true, "has": true,
	"have": true, "how": true, "in": true, "is": true, "it": true, "its": true,
	"not": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "was": true, "what": true, "when": true, "which": true,
	"with": true, "you": true, "your": true,
}

// words splits lowercased alphanumeric words of at least min length,
// minus stopwords. min <= 0 keeps every word.
func words(text string, min int) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) >= min && !stopwords[w] {
			out[w] = true
		}
	}
	return out
}

// rooted matches inflections: either word contains the other, so
// refund finds refunds. Both words cleared the length floor.
func rooted(ew map[string]bool, w string) bool {
	for e := range ew {
		if strings.Contains(e, w) || strings.Contains(w, e) {
			return true
		}
	}
	return false
}

type scoredEntry struct {
	entry *Entry
	score int
}

func rank(entries []*Entry, query string, minWord int) []scoredEntry {
	queryWords := words(query, minWord)
	scored := make([]scoredEntry, len(entries))
	for i, e := range entries {
		scored[i].entry = e
		entryWords := words(e.Key+"\n"+e.FilePath+"\n"+e.Body, minWord)
		for w := range queryWords {
			if entryWords[w] || rooted(entryWords, w) {
				scored[i].score++
			}
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	return scored
}

// Rank orders entries by distinct query-word overlap in their key, path and
// body. Scores are computed once; ties keep the input's newest-first order.
func Rank(entries []*Entry, query string, minWord int) []*Entry {
	ranked := rank(entries, query, minWord)
	out := make([]*Entry, len(ranked))
	for i, scored := range ranked {
		out[i] = scored.entry
	}
	return out
}

// Recall ranks entries against the query and formats the top matches:
// at most topN, complete returned text within maxChars Unicode runes.
// topN <= 0 disables; maxChars <= 0 lifts the cap. Prefer whole labelled
// blocks, then whole compact bodies if labels cannot fit. Budget omissions
// end in a separate ellipsis line; routine topN selection does not add one.
// If no whole body fits, return the top head plus an ellipsis (cap1: only …).
func Recall(entries []*Entry, query string, topN, maxChars, minWord int, catalogs ...*prompts.Catalog) string {
	if topN <= 0 {
		return ""
	}
	ranked := rank(entries, query, minWord)
	var matches []*Entry
	for _, scored := range ranked {
		if scored.score == 0 {
			break
		}
		matches = append(matches, scored.entry)
	}
	if len(matches) == 0 {
		return ""
	}
	for _, compact := range []bool{false, true} {
		if text := recallBlocks(matches, topN, maxChars, compact, catalogs...); text != "" {
			return text
		}
	}
	body := []rune(matches[0].Body)
	n := maxChars - 1 // reserve the one-rune truncation disclosure
	if n < 0 {
		n = 0
	}
	if n > len(body) {
		n = len(body)
	}
	return string(body[:n]) + "…"
}

// Keep whole blocks in rank order, skipping oversized ones. Selection is
// repeated only when a budget omission needs space for its newline+marker.
// The private selector uses -1 for unlimited; zero is an exhausted budget.
func recallBlocks(entries []*Entry, topN, maxChars int, compact bool, catalogs ...*prompts.Catalog) string {
	pc := prompts.Select(catalogs...)
	prefix := pc.Text("continuation.recall_prefix")
	if compact {
		prefix = ""
	}
	pick := func(limit int) ([]string, bool) {
		var blocks []string
		used, omitted := utf8.RuneCountInString(prefix), false
		for _, e := range entries {
			if len(blocks) >= topN {
				break
			}
			block := e.Body
			if !compact {
				label := e.Kind
				if e.Key != "" {
					label += fmt.Sprintf(" key=%q", e.Key)
				}
				if e.FilePath != "" {
					label += fmt.Sprintf(" file=%q", e.FilePath)
				}
				block = fmt.Sprintf("- [%s] %s", label, e.Body)
			}
			size := utf8.RuneCountInString(block)
			if len(blocks) > 0 {
				size++ // the joining newline is part of returned text
			}
			if limit >= 0 && used+size > limit {
				omitted = true
				continue
			}
			blocks = append(blocks, block)
			used += size
		}
		return blocks, omitted
	}
	limit := maxChars
	if limit <= 0 {
		limit = -1
	}
	blocks, omitted := pick(limit)
	if omitted && len(blocks) > 0 {
		if maxChars < 2 {
			return ""
		}
		blocks, _ = pick(maxChars - 2)
	}
	if len(blocks) == 0 {
		return ""
	}
	text := prefix + strings.Join(blocks, "\n")
	if omitted {
		text += "\n…"
	}
	return text
}

// Query recalls legacy global entries. Project-bound callers use QueryProject
// so authored knowledge follows the same ranking and output budget.
func Query(s *store.Store, kind, query string, topN, maxChars, minWord int, catalogs ...*prompts.Catalog) (string, error) {
	entries, err := List(s, kind, "", 0)
	if err != nil {
		return "", err
	}
	return Recall(entries, query, topN, maxChars, minWord, catalogs...), nil
}

// QueryProject recalls current authored notes for the captured workspace plus
// legacy global entries. Guides remain available on demand; old versions and
// other projects never compete for the caller's existing recall budget.
func QueryProject(ctx context.Context, s *store.Store, root, kind, query string, topN, maxChars, minWord int, catalogs ...*prompts.Catalog) (string, error) {
	if root == "" {
		return "", fmt.Errorf("project recall requires a captured workspace")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if topN <= 0 || len(words(query, minWord)) == 0 {
		return "", nil
	}
	q := `MATCH (m:MemoryEntry) WHERE (
coalesce(m.project_root,'') = '' OR (
m.project_root = $root AND m.kind = 'note'
AND NOT (coalesce(m.key,'') STARTS WITH 'harness/reference/')
AND NOT EXISTS { MATCH (n:MemoryEntry) WHERE n.previous_id = m.id }
))`
	params := map[string]any{"root": root}
	if kind != "" {
		q += " AND m.kind = $kind"
		params["kind"] = kind
	}
	q += ` RETURN m.id AS id, m.kind AS kind, m.body AS body, m.key AS key, m.file_path AS file_path,
m.created_at AS created_at, m.session_ref AS ref ORDER BY created_at DESC, id`
	rows, err := s.QueryContext(ctx, q, params)
	if err != nil {
		return "", err
	}
	entries := make([]*Entry, 0, len(rows))
	for _, row := range rows {
		entry, err := entryOf(row)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry)
	}
	return Recall(entries, query, topN, maxChars, minWord, catalogs...), nil
}
