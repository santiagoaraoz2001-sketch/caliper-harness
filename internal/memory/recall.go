package memory

import (
	"context"
	"fmt"
	"regexp"
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
	"user": true, "project": true, "note": true, "notes": true, "memory": true, "before": true, "after": true, "please": true, "like": true, "tell": true, "about": true, "could": true, "would": true, "should": true, "want": true, "need": true, "using": true, "use": true, "without": true, "just": true, "only": true,
	"change": true, "changed": true, "changes": true, "check": true, "checks": true, "command": true, "commands": true, "file": true, "files": true, "line": true, "lines": true, "task": true, "tasks": true, "tool": true, "tools": true, "work": true, "working": true, "question": true, "questions": true,
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

// Match inflections, not arbitrary substrings ("line" must not match "pipeline").
func rooted(ew map[string]bool, w string) bool {
	for e := range ew {
		if len([]rune(w)) == 1 && []rune(w)[0] > 127 && strings.Contains(e, w) {
			return true
		}
		for _, suffix := range []string{"s", "es", "ed", "ing"} {
			if e == w+suffix || w == e+suffix {
				return true
			}
		}
	}
	return false
}

var recallPaths = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|/)[^\s]+`)
var recallNegation = regexp.MustCompile(`(?i)\b(?:do not|don't|must not|without|never|no)\s+(?:run|running|edit|editing|change|changing|modify|modifying|execute|executing|build|building|test|testing|check|checking)\b[^,.!?;\n]*(?:,\s*(?:run|edit|change|modify|execute|build|test|check|use)\b[^,.!?;\n]*)*`)

// Negated action constraints and generated labels are not positive memory intent.
// Exact file locators are handled separately, not tokenized as generic topics.
func recallQuery(query string) string {
	query = recallPaths.ReplaceAllString(query, " ")
	return recallNegation.ReplaceAllString(query, " ")
}

type scoredEntry struct {
	entry *Entry
	score int
}

func rank(entries []*Entry, query string, minWord int) []scoredEntry {
	positiveQuery := recallNegation.ReplaceAllString(query, " ")
	queryWords := words(recallQuery(positiveQuery), minWord)
	scored := make([]scoredEntry, len(entries))
	for i, e := range entries {
		scored[i].entry = e
		if e.FilePath != "" && strings.Contains(strings.ToLower(strings.ReplaceAll(positiveQuery, `\`, "/")), strings.ToLower(strings.ReplaceAll(e.FilePath, `\`, "/"))) {
			scored[i].score++
		}
		entryWords := words(e.Key+"\n"+recallPaths.ReplaceAllString(e.Body, " "), minWord)
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

// Recall keeps historical provenance and a stable locator inside the same budget.
// A budget too small for provenance omits memory instead of presenting bare text
// as a new user instruction. Raw records remain accessible through GraphQL.
func Recall(entries []*Entry, query string, topN, maxChars, minWord int, catalogs ...*prompts.Catalog) string {
	_, text := SelectRecall(entries, nil, query, topN, maxChars, minWord, catalogs...)
	return text
}

// RecallRecord retains only a selected, possibly truncated body. Its original
// score survives truncation so a cached fallback does not need to re-match text
// which may have been outside the retained fragment.
type RecallRecord struct {
	Entry *Entry
	Score int
}

// SelectRecall shares Recall's ranking and budget with structured context
// owners. Cached records are used only when fresh selection is unavailable.
func SelectRecall(entries []*Entry, cached []RecallRecord, query string, topN, maxChars, minWord int, catalogs ...*prompts.Catalog) ([]RecallRecord, string) {
	if topN <= 0 {
		return nil, ""
	}
	candidates := rank(entries, query, minWord)
	for _, record := range cached {
		candidates = append(candidates, scoredEntry{entry: record.Entry, score: record.Score})
	}
	if len(cached) > 0 {
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	}
	prefix := prompts.Select(catalogs...).Text("continuation.recall_prefix")
	var out strings.Builder
	out.WriteString(prefix)
	used, count := utf8.RuneCountInString(prefix), 0
	selected := []RecallRecord{}
	seen := map[string]bool{}
	for _, scored := range candidates {
		if scored.score == 0 || count >= topN {
			break
		}
		e := scored.entry
		// Domain articles are retrieved as whole bounded sections through their
		// explicit graph links, not cropped into the ordinary fact-recall budget.
		if ProjectGuide(e.Key) {
			continue
		}
		if len(cached) > 0 && e.ID != "" && seen[e.ID] {
			continue
		}
		label := e.Kind
		if e.Key != "" {
			label += fmt.Sprintf(" key=%q", e.Key)
		} else if e.ID != "" {
			label += fmt.Sprintf(" id=%q", e.ID)
		}
		if e.FilePath != "" {
			label += fmt.Sprintf(" file=%q", e.FilePath)
		}
		header := "- [" + label + "] "
		if count > 0 {
			header = "\n" + header
		}
		headerSize := utf8.RuneCountInString(header)
		body := []rune(e.Body)
		if maxChars > 0 && used+headerSize+len(body) > maxChars {
			room := maxChars - used - headerSize - 1
			if room < 1 {
				continue
			}
			body = append(body[:room], '…')
		}
		out.WriteString(header)
		out.WriteString(string(body))
		bounded := *e
		bounded.Body = string(body)
		selected = append(selected, RecallRecord{Entry: &bounded, Score: scored.score})
		seen[e.ID] = true
		used += headerSize + len(body)
		count++
	}
	if count == 0 {
		return nil, ""
	}
	return selected, out.String()
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
	entries, err := ProjectRecallEntries(ctx, s, root, kind, query, topN, minWord)
	if err != nil {
		return "", err
	}
	return Recall(entries, query, topN, maxChars, minWord, catalogs...), nil
}

// ProjectRecallEntries retains immutable identities until the caller has
// reconciled other current context owners. Recall still owns ranking/formatting.
func ProjectRecallEntries(ctx context.Context, s *store.Store, root, kind, query string, topN, minWord int) ([]*Entry, error) {
	if root == "" {
		return nil, fmt.Errorf("project recall requires a captured workspace")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topN <= 0 || len(words(query, minWord)) == 0 {
		return nil, nil
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
	q += ` RETURN m.id AS id, m.kind AS kind, m.body AS body, m.key AS key, m.file_path AS file_path, m.project_root AS project_root,
m.created_at AS created_at, m.session_ref AS ref ORDER BY created_at DESC, id`
	rows, err := s.QueryContext(ctx, q, params)
	if err != nil {
		return nil, err
	}
	entries := make([]*Entry, 0, len(rows))
	for _, row := range rows {
		entry, err := entryOf(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
