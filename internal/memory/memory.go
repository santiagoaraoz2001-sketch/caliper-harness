// Package memory owns memory-entry logic over the store. GraphQL resolvers
// are thin wrappers over it — the one file memory edits concentrate on.
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/plan"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"specific-harness/internal/store"
)

// Entry is one memory row.
type Entry struct {
	ProjectRoot, Key, PreviousID, TaskID, FilePath string
	ID                                             string
	Kind                                           string
	Body                                           string
	CreatedAt                                      time.Time
	SessionRef                                     string
}

// ValidKinds are the only memory kinds, in the order errors name them.
const ValidKinds = "event, decision, note"

// Add stores one entry. Unknown kinds are rejected plainly.
func Add(s *store.Store, kind, body, ref string) (*Entry, error) {
	return AddContext(context.Background(), s, kind, body, ref)
}
func AddContext(ctx context.Context, s *store.Store, kind, body, ref string) (*Entry, error) {
	switch kind {
	case "event", "decision", "note":
	default:
		return nil, fmt.Errorf("memory kind must be one of %s, got %q", ValidKinds, kind)
	}
	e := &Entry{ID: uuid.NewString(), Kind: kind, Body: body, CreatedAt: time.Now().UTC(), SessionRef: ref}
	err := s.WriteContext(ctx,
		`CREATE (m:MemoryEntry {id: $id, kind: $kind, body: $body, created_at: $now, session_ref: $ref})`,
		map[string]any{"id": e.ID, "kind": kind, "body": body, "now": e.CreatedAt, "ref": ref},
	)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// List returns entries newest-first, narrowed by kind (exact match), text
// (body substring), and limit (max rows). Empty kind/text and limit <= 0
// mean unfiltered. Filters bind as params; only the integer limit is
// formatted in, which cannot inject.
func List(s *store.Store, kind, text string, limit int) ([]*Entry, error) {
	q := `MATCH (m:MemoryEntry)`
	params := map[string]any{}
	conds := []string{"coalesce(m.project_root,'') = ''"}
	if kind != "" {
		conds = append(conds, "m.kind = $kind")
		params["kind"] = kind
	}
	if text != "" {
		conds = append(conds, "m.body CONTAINS $text")
		params["text"] = text
	}

	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += ` RETURN m.id AS id, m.kind AS kind, m.body AS body, m.created_at AS created_at, m.session_ref AS ref ORDER BY created_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.Query(q, params)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(rows))
	for _, r := range rows {
		e, err := entryOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// entryOf maps one store row. It detects shape drift (renamed columns come
// back nil); it does not pretend to validate our own schema's types.
func entryOf(r map[string]any) (*Entry, error) {
	id, _ := r["id"].(string)
	kind, _ := r["kind"].(string)
	body, _ := r["body"].(string)
	ref, _ := r["ref"].(string)
	ts, ok := r["created_at"].(time.Time)
	if id == "" || !ok {
		return nil, fmt.Errorf("memory row has wrong shape: %v", r)
	}
	e := &Entry{ID: id, Kind: kind, Body: body, CreatedAt: ts, SessionRef: ref}
	e.ProjectRoot, _ = r["project_root"].(string)
	e.Key, _ = r["key"].(string)
	e.PreviousID, _ = r["previous_id"].(string)
	e.TaskID, _ = r["task_id"].(string)
	e.FilePath, _ = r["file_path"].(string)
	return e, nil
}

// GraphList projects selected strings before returning native rows.
func GraphList(ctx context.Context, s *store.Store, kind, text string, limit, bytes int, body bool) ([]*Entry, error) {
	return graphList(ctx, s, kind, text, limit, bytes, body, "", "", "", nil)
}
func graphList(ctx context.Context, s *store.Store, kind, text string, limit, bytes int, body bool, root, key, id string, fields []string, relevance ...KnowledgeSelection) ([]*Entry, error) {
	q := `MATCH(m:MemoryEntry)`
	params := map[string]any{}
	conds := []string{"coalesce(m.project_root,'') = ''"}
	if kind != "" {
		conds = append(conds, "m.kind=$kind")
		params["kind"] = kind
	}
	if text != "" {
		conds = append(conds, "m.body CONTAINS $text")
		params["text"] = text
	}
	if root != "" {
		params["root"] = root
		conds = []string{"m.project_root=$root", "m.kind='note'"}
		if id != "" {
			conds = append(conds, "m.id=$id")
			params["id"] = id
		} else {
			conds = append(conds, "NOT EXISTS { MATCH(n:MemoryEntry) WHERE n.previous_id=m.id }")
			if key != "" {
				conds = append(conds, "m.key=$key")
				params["key"] = key
			}
		}
	}
	if len(relevance) > 0 {
		chosen := relevance[0]
		matches := []string{}
		for _, pair := range []struct{ column, value string }{{"task_id", chosen.TaskID}, {"file_path", chosen.FilePath}, {"session_ref", chosen.SessionRef}} {
			if pair.value != "" {
				matches = append(matches, "m."+pair.column+"=$"+pair.column)
				params[pair.column] = pair.value
			}
		}
		if len(matches) == 0 {
			return []*Entry{}, ctx.Err()
		}
		conds = append(conds, "("+strings.Join(matches, " OR ")+")")
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	cols := []string{"id", "kind", "session_ref"}
	if root != "" {
		cols = append(cols, "key", "previous_id", "task_id", "file_path")
		if fields != nil {
			cols = []string{"id"}
			names := map[string]string{"key": "key", "sessionRef": "session_ref", "previousId": "previous_id", "taskId": "task_id", "filePath": "file_path"}
			seen := map[string]bool{}
			for _, field := range fields {
				if c := names[field]; c != "" && !seen[c] {
					cols = append(cols, c)
					seen[c] = true
				}
			}
		}
	}
	if body {
		cols = append(cols, "body")
	}
	size := []string{}
	for _, c := range cols {
		size = append(size, "coalesce(size(m."+c+"),0)*4")
	}
	total := strings.Join(size, "+")
	if bytes > 0 {
		q += " WITH m ORDER BY m.created_at DESC,m.id"
		if limit > 0 {
			q += fmt.Sprintf(" LIMIT %d", limit)
		}
		q += " WITH collect(m) AS nodes, sum(" + total + ") AS source_size UNWIND nodes AS m"
		total = "source_size"
	}
	ret := []string{"m.created_at AS created_at"}
	for _, c := range cols {
		v := "m." + c
		if bytes > 0 {
			v = "CASE WHEN (" + total + ") <= $bytes THEN " + v + " ELSE NULL END"
		}
		alias := c
		if c == "session_ref" {
			alias = "ref"
		}
		ret = append(ret, v+" AS "+alias)
	}
	if bytes > 0 {
		params["bytes"] = bytes
		ret = append(ret, "("+total+") > $bytes AS too_large")
	}
	q += " RETURN " + strings.Join(ret, ",") + " ORDER BY created_at DESC,id"
	if limit > 0 && bytes <= 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.QueryContext(ctx, q, params)
	if err != nil {
		return nil, err
	}
	out := []*Entry{}
	for _, r := range rows {
		if r["too_large"] == true {
			return nil, fmt.Errorf("GraphQL selected memory exceeds source byte budget; select narrower fields")
		}
		e, err := entryOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// PutKnowledge appends an authored note; CAS and typed links share the writer.
func PutKnowledge(ctx context.Context, s *store.Store, root, sessionRef, key, body, previous, taskID, filePath string) (*Entry, error) {
	if strings.HasPrefix(key, plan.RevisionKeyPrefix) {
		return nil, fmt.Errorf("knowledge key is reserved for owning revision receipts")
	}
	if root == "" || key == "" || !utf8.ValidString(key) || !utf8.ValidString(body) {
		return nil, fmt.Errorf("knowledge needs a captured project, nonempty UTF-8 key and UTF-8 body")
	}
	if filePath != "" {
		path := filePath
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		// Resolve existing parent directories, permitting new files but not escape.
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return nil, fmt.Errorf("knowledge file parent: %w", err)
		}
		path = filepath.Join(parent, filepath.Base(path))
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("knowledge final file symlink refused")
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("knowledge file link is outside captured project")
		}
		filePath = rel
	}
	e := &Entry{ID: uuid.NewString(), Kind: "note", Body: body, CreatedAt: time.Now().UTC(), SessionRef: sessionRef, ProjectRoot: root, Key: key, PreviousID: previous, TaskID: taskID, FilePath: filePath}
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if taskID != "" {
			if err := plan.CheckProject(tx, "task", taskID, root); err != nil {
				return err
			}
		}
		rows, err := tx.Query(`MATCH(m:MemoryEntry) WHERE m.project_root=$root AND m.key=$key AND NOT EXISTS { MATCH(n:MemoryEntry) WHERE n.previous_id=m.id } RETURN m.id AS id`, map[string]any{"root": root, "key": key})
		if err != nil {
			return err
		}
		head := ""
		if len(rows) > 1 {
			return fmt.Errorf("knowledge key has multiple heads; repair explicitly")
		}
		if len(rows) == 1 {
			head, _ = rows[0]["id"].(string)
		}
		if head != previous {
			return fmt.Errorf("knowledge changed: expected previous %q, actual head %q — use this current head ID; query the current body only if needed", previous, head)
		}
		return tx.Write(`CREATE(m:MemoryEntry {id:$id,kind:'note',body:$body,created_at:$now,session_ref:$ref,project_root:$root,key:$key,previous_id:$previous,task_id:$task,file_path:$file})`, map[string]any{"id": e.ID, "body": body, "now": e.CreatedAt, "ref": sessionRef, "root": root, "key": key, "previous": previous, "task": taskID, "file": filePath})
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}
func Knowledge(ctx context.Context, s *store.Store, root, key, id string, limit, bytes int, body bool, fields ...[]string) ([]*Entry, error) {
	if root == "" {
		return nil, fmt.Errorf("knowledge project scope unavailable")
	}
	if key != "" && id != "" {
		return nil, fmt.Errorf("knowledge accepts key or id, not both")
	}
	var projection []string
	if len(fields) > 0 {
		projection = fields[0]
	}
	return graphList(ctx, s, "", "", limit, bytes, body, root, key, id, projection)
}

// KnowledgeSelection names actual owning associations, never a guessed semantic match.
type KnowledgeSelection struct{ TaskID, FilePath, SessionRef string }

func SelectedKnowledge(ctx context.Context, s *store.Store, root string, selection KnowledgeSelection, rows, bytes int) ([]*Entry, error) {
	if root == "" || rows <= 0 || bytes <= 0 {
		return nil, fmt.Errorf("selected knowledge scope/budget unavailable")
	}
	entries, err := graphList(ctx, s, "", "", rows+1, bytes, true, root, "", "", []string{"key", "taskId", "filePath"}, selection)
	if err == nil && len(entries) > rows {
		return nil, fmt.Errorf("selected knowledge exceeds aggregate row budget")
	}
	return entries, err
}
