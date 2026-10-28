package memory

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"specific-harness/internal/pathutil"
	"strings"
	"unicode"

	"specific-harness/internal/store"
)

const UIStandardKey = "caliper/standards/ui"
const CodeStandardKey = "caliper/standards/code"
const TestsStandardKey = "caliper/standards/tests"

//go:embed code-standard.md
var CodeStandard string

//go:embed ui-standard.md
var UIStandard string

//go:embed tests-standard.md
var TestsStandard string

type Section struct{ ID, Title, Body string }
type Reference struct {
	Key           string `json:"key"`
	Relation      string `json:"relation"`
	Section       string `json:"section,omitempty"`
	SourceSection string `json:"sourceSection,omitempty"`
	Label         string `json:"label,omitempty"`
	Status        string `json:"status,omitempty"`
}

func markdownFence(line, fence string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
		return fence, false
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if fence == "" {
		return line[:n], true
	}
	if n >= len(fence) && line[0] == fence[0] && strings.TrimSpace(line[n:]) == "" {
		return "", true
	}
	return fence, true
}

// Sections supports normal Markdown headings; fenced code is never a heading.
// Anchors are stable slugs, with deterministic suffixes for repeated headings.
func Sections(body, text, anchor string) []Section {
	out := []Section{}
	current := Section{ID: "intro"}
	seen := map[string]int{}
	used := map[string]bool{}
	fence := ""
	flush := func() {
		current.Body = strings.TrimSpace(current.Body)
		if current.Body != "" && (anchor == "" || current.ID == anchor) && (text == "" || strings.Contains(strings.ToLower(current.Title+"\n"+current.Body), strings.ToLower(text))) {
			out = append(out, current)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		fence, _ = markdownFence(line, fence)
		if fence == "" && strings.HasPrefix(line, "#") {
			i := 0
			for i < len(line) && line[i] == '#' {
				i++
			}
			if i <= 6 && i < len(line) && line[i] == ' ' {
				flush()
				title := strings.TrimSpace(line[i:])
				slug := strings.Trim(strings.Map(func(r rune) rune {
					if unicode.IsLetter(r) || unicode.IsDigit(r) {
						return unicode.ToLower(r)
					}
					return '-'
				}, title), "-")
				for strings.Contains(slug, "--") {
					slug = strings.ReplaceAll(slug, "--", "-")
				}
				if slug == "" {
					slug = "section"
				}
				seen[slug]++
				id := slug
				if seen[slug] > 1 {
					id = fmt.Sprintf("%s-%d", slug, seen[slug])
				}
				for used[id] {
					seen[slug]++
					id = fmt.Sprintf("%s-%d", slug, seen[slug])
				}
				used[id] = true
				current = Section{ID: id, Title: title}
			}
		}
		current.Body += line + "\n"
	}
	flush()
	return out
}

func BuiltinKnowledge(key, id, text string) *Entry {
	for _, candidate := range BuiltinKeys {
		body, prefix := builtinBodies[candidate], "caliper-guide-"
		switch candidate {
		case UIStandardKey:
			prefix = "caliper-ui-"
		case CodeStandardKey:
			prefix = "caliper-code-"
		case TestsStandardKey:
			prefix = "caliper-tests-"
		}
		version := fmt.Sprintf("%s%x", prefix, sha256.Sum256([]byte(body)))
		if key != "" && key != candidate || id != "" && id != version {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(candidate+"\n"+body), strings.ToLower(text)) {
			continue
		}
		return &Entry{ID: version, Key: candidate, Kind: "note", Body: body, SessionRef: "caliper-default"}
	}
	return nil
}

func SearchKnowledge(ctx context.Context, s *store.Store, root, key, id string, limit, bytes int, body bool, fields []string, filter KnowledgeSelection) ([]*Entry, error) {
	if root == "" {
		return nil, fmt.Errorf("knowledge project scope unavailable")
	}
	if key != "" && id != "" {
		return nil, fmt.Errorf("knowledge accepts key or id, not both")
	}
	if filter.Offset < 0 || filter.Offset > 100000 {
		return nil, fmt.Errorf("knowledge offset must be 0..100000")
	}
	if filter.LinkedFrom != "" && filter.LinkedTo != "" {
		return nil, fmt.Errorf("choose linkedFrom or linkedTo")
	}
	filter.Filter = true
	return graphList(ctx, s, "", "", limit, bytes, body, root, key, id, fields, filter)
}

func PutKnowledgeReferences(ctx context.Context, s *store.Store, root, ref, key, body, previous, task, file string, refs []Reference) (*Entry, error) {
	return putKnowledge(ctx, s, root, ref, key, body, previous, task, file, "", refs)
}

// KnowledgeRelations is the canonical accepted relation vocabulary. Return a
// fresh slice so help callers cannot mutate validation policy.
func KnowledgeRelations() []string {
	return []string{"implements", "depends_on", "verifies", "explains"}
}

func validateReferences(refs []Reference) error {
	if len(refs) > 32 {
		return fmt.Errorf("knowledge supports at most 32 direct references; link a focused document")
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if strings.TrimSpace(r.Key) == "" || len(r.Key) > 512 || len(r.Section) > 128 || len(r.SourceSection) > 128 || len(r.Label) > 512 {
			return fmt.Errorf("reference needs key (1..512 bytes), optional section/sourceSection (<=128 bytes), and optional label (<=512 bytes)")
		}
		valid := false
		for _, relation := range KnowledgeRelations() {
			valid = valid || r.Relation == relation
		}
		if !valid {
			return fmt.Errorf("reference relation must be one of: %s", strings.Join(KnowledgeRelations(), ", "))
		}
		identity := r.Key + "\x00" + r.Relation + "\x00" + r.Section + "\x00" + r.SourceSection
		if seen[identity] {
			return fmt.Errorf("duplicate knowledge reference")
		}
		seen[identity] = true
	}
	return nil
}

func saveReferences(tx *store.Tx, root, id, previous string, refs []Reference) error {
	if refs == nil && previous != "" {
		return tx.Write(`MATCH(old:MemoryEntry)-[r:KNOWLEDGE_REF]->(t:KnowledgeTarget),(fresh:MemoryEntry) WHERE old.id=$old AND old.project_root=$root AND fresh.id=$id AND t.project_root=$root CREATE(fresh)-[:KNOWLEDGE_REF {relation:r.relation,section:r.section,source_section:r.source_section,label:r.label}]->(t)`, map[string]any{"old": previous, "root": root, "id": id})
	}
	for _, r := range refs {
		target := fmt.Sprintf("%x", sha256.Sum256([]byte(root+"\x00"+r.Key)))
		args := map[string]any{"target": target, "root": root, "key": r.Key}
		if err := tx.Write(`MERGE(t:KnowledgeTarget {id:$target}) ON CREATE SET t.project_root=$root,t.key=$key`, args); err != nil {
			return err
		}
		args = map[string]any{"id": id, "target": target, "root": root, "relation": r.Relation, "section": r.Section, "source": r.SourceSection, "label": r.Label}
		if err := tx.Write(`MATCH(m:MemoryEntry),(t:KnowledgeTarget) WHERE m.id=$id AND m.project_root=$root AND t.id=$target CREATE(m)-[:KNOWLEDGE_REF {relation:$relation,section:$section,source_section:$source,label:$label}]->(t)`, args); err != nil {
			return err
		}
	}
	return nil
}

func References(ctx context.Context, s *store.Store, root, id string, resolveStatus bool, maxBytes int, consume func(string) error, anchors ...string) ([]Reference, error) {
	rows, err := s.QueryContext(ctx, `MATCH(m:MemoryEntry)-[r:KNOWLEDGE_REF]->(t:KnowledgeTarget) WHERE m.id=$id AND m.project_root=$root AND t.project_root=$root RETURN t.key AS key,r.relation AS relation,r.section AS section,r.source_section AS source_section,r.label AS label ORDER BY key,relation,section,source_section LIMIT 33`, map[string]any{"id": id, "root": root})
	if err != nil {
		return nil, err
	}
	if len(rows) > 32 {
		return nil, fmt.Errorf("knowledge references exceed 32; narrow source document")
	}
	if len(anchors) > 0 {
		selected := rows[:0]
		for _, row := range rows {
			source, _ := row["source_section"].(string)
			include := source == ""
			for _, anchor := range anchors {
				include = include || source == anchor
			}
			if include {
				selected = append(selected, row)
			}
		}
		rows = selected
	}
	refs := make([]Reference, 0, len(rows))
	// A target can have several section/relationship edges. Read and charge its
	// source once in this call; never retain cached state across graph requests.
	needsBody := map[string]bool{}
	for _, row := range rows {
		key, _ := row["key"].(string)
		section, _ := row["section"].(string)
		needsBody[key] = needsBody[key] || section != ""
	}
	targets := map[string]*Entry{}
	for _, row := range rows {
		r := Reference{}
		r.Key, _ = row["key"].(string)
		r.Relation, _ = row["relation"].(string)
		r.Section, _ = row["section"].(string)
		r.SourceSection, _ = row["source_section"].(string)
		r.Label, _ = row["label"].(string)
		if !resolveStatus {
			refs = append(refs, r)
			continue
		}
		r.Status = "missing"
		target, cached := targets[r.Key]
		if cached {
			// Includes missing targets: one failed lookup is enough per call.
		} else if strings.HasPrefix(r.Key, "caliper/") {
			target = BuiltinKnowledge(r.Key, "", "")
		} else {
			if maxBytes <= 0 {
				return nil, fmt.Errorf("knowledge reference source budget exhausted")
			}
			entries, e := Knowledge(ctx, s, root, r.Key, "", 1, maxBytes, needsBody[r.Key], []string{"key"})
			if e != nil {
				return nil, e
			}
			if len(entries) > 0 {
				target = entries[0]
				maxBytes -= len(target.Body) + len(target.Key) + len(target.ID)
				if consume != nil {
					if err := consume(target.Body + target.Key + target.ID); err != nil {
						return nil, err
					}
				}
			}
		}
		targets[r.Key] = target
		if target != nil {
			r.Status = "resolved"
			if r.Section != "" && len(Sections(target.Body, "", r.Section)) == 0 {
				r.Status = "missing-section"
			}
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// Only the explicitly linked regular file is read. Resolve symlinks again at
// lookup time; a later link replacement must not read outside the project.
func fingerprint(root, path string) (string, string) {
	if path == "" {
		return "", "unknown"
	}
	resolved, err := pathutil.EvalSymlinks(filepath.Join(root, path))
	if os.IsNotExist(err) {
		return "", "missing"
	}
	if err != nil {
		return "", "unknown"
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "unknown"
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", "unknown"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return "", "unknown"
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (8<<20)+1))
	if err != nil || n > 8<<20 {
		return "", "unknown"
	}
	return fmt.Sprintf("%x", h.Sum(nil)), "present"
}

func Freshness(root string, e *Entry) string {
	current, status := fingerprint(root, e.FilePath)
	if status != "present" {
		return status
	}
	if e.FileSHA256 == "" {
		return "unknown"
	}
	if current == e.FileSHA256 {
		return "unchanged"
	}
	return "changed"
}

// LinkedBuiltins returns only the bounded bundled targets of a current project
// document. Missing targets remain visible on references, not invented nodes.
func LinkedBuiltins(ctx context.Context, s *store.Store, root, key, relation string) (map[string]bool, error) {
	if strings.HasPrefix(key, "caliper/") {
		out := map[string]bool{}
		for _, ref := range BuiltinReferences(key, false) {
			if relation == "" || relation == ref.Relation {
				out[ref.Key] = true
			}
		}
		return out, nil
	}
	rows, err := s.QueryContext(ctx, `MATCH(m:MemoryEntry)-[r:KNOWLEDGE_REF]->(t:KnowledgeTarget) WHERE m.project_root=$root AND m.key=$key AND NOT EXISTS {MATCH(n:MemoryEntry) WHERE n.previous_id=m.id} AND t.project_root=$root AND t.key STARTS WITH 'caliper/' AND ($relation='' OR r.relation=$relation) RETURN DISTINCT t.key AS key LIMIT 33`, map[string]any{"root": root, "key": key, "relation": relation})
	out := map[string]bool{}
	for _, row := range rows {
		if key, ok := row["key"].(string); ok {
			out[key] = true
		}
	}
	return out, err
}
