package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"specific-harness/internal/store"
	"strings"
	"unicode/utf8"
)

// VisitAttemptRecords streams the existing log through bounded Kuzu string
// slices. Each read rechecks project ownership and source identity; concurrent
// replacement refuses rather than combining evidence from different logs.
func VisitAttemptRecords(ctx context.Context, s *store.Store, id string, bytes int, visit func(int, json.RawMessage) error) (int, error) {
	if ProjectRoot(ctx) == "" || bytes < utf8.UTFMax {
		return 0, fmt.Errorf("task evidence needs captured project scope and available GraphQL source bytes")
	}
	const match = `MATCH (p:Plan)-[:HAS_APPROACH]->(:Approach)-[:HAS_TASK]->(t:Task) WHERE p.project_root=$root AND t.id=$id`
	params := map[string]any{"id": id, "root": ProjectRoot(ctx)}
	rows, err := s.QueryContext(ctx, match+` RETURN hash(t.attempt_log) AS digest, size(t.attempt_log) AS length`, params)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, fmt.Errorf("task %q is missing, unbound or outside captured project", id)
	}
	params["digest"], params["length"] = rows[0]["digest"], rows[0]["length"]
	length, ok := rows[0]["length"].(int64)
	if !ok || length < 0 {
		return 0, fmt.Errorf("task %q has unavailable attempt source length", id)
	}
	reader := &attemptReader{ctx: ctx, store: s, query: match + ` AND hash(t.attempt_log)=$digest AND size(t.attempt_log)=$length RETURN substring(t.attempt_log,$start,$count) AS part`, params: params, count: bytes / utf8.UTFMax, offset: 1, length: int(length)}
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return 0, fmt.Errorf("task %q has a corrupt attempt log: %v", id, err)
	}
	n := 0
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return 0, fmt.Errorf("task %q has corrupt record %d: %w", id, n, err)
		}
		if err := visit(n, raw); err != nil {
			return 0, err
		}
		n++
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim(']') {
		return 0, fmt.Errorf("task %q has a corrupt attempt log: %v", id, err)
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return 0, fmt.Errorf("task %q has trailing or changed attempt evidence: %v", id, err)
	}
	return n, ctx.Err()
}

type attemptReader struct {
	ctx    context.Context
	store  *store.Store
	query  string
	params map[string]any
	part   *strings.Reader
	count  int
	offset int
	length int
}

func (r *attemptReader) Read(dst []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.part != nil && r.part.Len() > 0 {
		return r.part.Read(dst)
	}
	if r.offset > r.length {
		return 0, io.EOF
	}
	r.params["start"], r.params["count"] = r.offset, min(r.count, r.length-r.offset+1)
	rows, err := r.store.QueryContext(r.ctx, r.query, r.params)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, fmt.Errorf("task evidence changed or project association is unavailable; retry the selected read")
	}
	part := stringField(rows[0], "part")
	if part == "" {
		return 0, io.EOF
	}
	r.offset += utf8.RuneCountInString(part)
	r.part = strings.NewReader(part)
	return r.part.Read(dst)
}
