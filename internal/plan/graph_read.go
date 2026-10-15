package plan

import (
	"context"
	"fmt"
	"specific-harness/internal/store"
	"strings"
)

// GraphRows is the owner's selected PAT projection. Columns are schema-owned,
// not query text from a caller. Unselected large strings never cross into Go.
func GraphRows(ctx context.Context, s *store.Store, kind, id, parent, state string, fields []string, limit, bytes int) ([]map[string]any, error) {
	labels := map[string]string{"plan": "Plan", "approach": "Approach", "task": "Task"}
	label, ok := labels[kind]
	if !ok {
		return nil, fmt.Errorf("unknown PAT projection %q", kind)
	}
	columns := map[string]string{"projectRoot": "project_root", "title": "title", "goal": "goal", "prompt": "prompt", "current": "current", "target": "target", "sketch": "sketch", "acceptance": "acceptance", "tests": "tests", "verify": "verify", "checkCommand": "check_command", "acceptanceCommand": "acceptance_command", "result": "result", "review": "review", "execution": "execution", "executionReason": "execution_reason", "verification": "verification", "verificationReason": "verification_reason", "attemptLog": "attempt_log"}
	selected := map[string]int{"id": 1, "state": 1}
	for _, f := range fields {
		if c, ok := columns[f]; ok {
			selected[c]++
		}
	}
	if kind == "task" && contains(fields, "qualified") {
		for _, c := range []string{"check_command", "acceptance_command", "execution", "verification", "check_projection"} {
			selected[c]++
		}
	}
	q := "MATCH (x:" + label + ")"
	params := map[string]any{}
	conditions := []string{}
	if parent != "" {
		if kind == "approach" {
			q = "MATCH (p:Plan)-[:HAS_APPROACH]->(x:Approach)"
		} else if kind == "task" {
			q = "MATCH (p:Approach)-[:HAS_TASK]->(x:Task)"
		} else {
			return nil, fmt.Errorf("plan projection has no parent")
		}
		conditions = append(conditions, "p.id=$parent")
		params["parent"] = parent
	}
	if id != "" {
		conditions = append(conditions, "x.id=$id")
		params["id"] = id
	}
	if state != "" {
		if state == "!retired" {
			conditions = append(conditions, "x.state <> 'retired'")
		} else {
			conditions = append(conditions, "x.state=$state")
			params["state"] = state
		}
	}
	if root := ProjectRoot(ctx); root != "" {
		switch kind {
		case "plan":
			conditions = append(conditions, "x.project_root=$project")
		case "approach":
			q = "MATCH (p:Plan)-[:HAS_APPROACH]->(x:Approach)"
			conditions = append(conditions, "p.project_root=$project")
		case "task":
			q = "MATCH (owner:Plan)-[:HAS_APPROACH]->(p:Approach)-[:HAS_TASK]->(x:Task)"
			conditions = append(conditions, "owner.project_root=$project")
		}
		params["project"] = root
	}
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	size, returning := []string{}, []string{"x.created_at AS created_at"}
	for c, count := range selected {
		size = append(size, fmt.Sprintf("coalesce(size(x.%s),0)*4*%d", c, count))
	}
	total := strings.Join(size, "+")
	if bytes > 0 {
		q += " WITH x ORDER BY x.created_at,x.id"
		if limit > 0 {
			q += fmt.Sprintf(" LIMIT %d", limit)
		}
		q += " WITH collect(x) AS nodes, sum(" + total + ") AS source_size UNWIND nodes AS x"
		total = "source_size"
	}
	for c := range selected {
		value := "x." + c
		if bytes > 0 {
			value = "CASE WHEN (" + total + ") <= $bytes THEN " + value + " ELSE NULL END"
		}
		returning = append(returning, value+" AS "+c)
	}
	if kind == "approach" {
		returning = append(returning, "x.needs_ready AS needs_ready")
	}
	if kind == "task" {
		returning = append(returning, "x.attempts AS attempts")
	}
	if bytes > 0 {
		params["bytes"] = bytes
		returning = append(returning, "("+total+") > $bytes AS too_large", total+" AS source_size")
	}
	q += " RETURN " + strings.Join(returning, ",") + " ORDER BY created_at, id"
	if limit > 0 && bytes <= 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	return s.QueryContext(ctx, q, params)
}
func contains(fields []string, name string) bool {
	for _, f := range fields {
		if f == name {
			return true
		}
	}
	return false
}

// GraphPlan/Approach/Task reuse the existing typed owner mapping and outcomes.
func GraphPlan(row map[string]any) (*Plan, error)         { return planOf(row) }
func GraphApproach(row map[string]any) (*Approach, error) { return approachOf(row) }
func GraphTask(row map[string]any) (*Task, error) {
	t, err := taskOf(row)
	if t != nil {
		if projection, ok := row["check_projection"].(string); ok {
			t.checkProjection = &projection
		}
	}
	return t, err
}
