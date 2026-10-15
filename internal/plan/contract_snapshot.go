package plan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"specific-harness/internal/store"
	"strings"
)

// ContractSnapshot binds authored requirements and revision identity, never lifecycle
// state, results or child drafts. The complete JSON is also the approval brief.
func ContractSnapshot(ctx context.Context, s *store.Store, kind, id string) (digest, brief string, err error) {
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := CheckProject(tx, kind, id, ProjectRoot(ctx)); e != nil {
			return e
		}
		digest, brief, err = contractSnapshot(tx, kind, id, true)
		return err
	})
	return
}
func contractAncestry(s stateStore, kind, id string) (p *Plan, a *Approach, t *Task, err error) {
	switch kind {
	case "task":
		t, err = GetTask(s, id)
		if err == nil {
			a, err = taskApproach(s, id)
		}
	case "approach":
		a, err = GetApproach(s, id)
	case "plan":
		p, err = GetPlan(s, id)
	default:
		err = fmt.Errorf("unknown contract kind %q", kind)
	}
	if err == nil && a != nil {
		p, err = ApproachPlan(s, a.ID)
	}
	return
}
func contractSnapshot(s stateStore, kind, id string, revisions bool) (string, string, error) {
	p, a, t, err := contractAncestry(s, kind, id)
	if err != nil {
		return "", "", err
	}
	contract := map[string]any{"plan": map[string]string{"id": p.ID, "project_root": p.ProjectRoot, "title": p.Title, "goal": p.Goal}}
	if a != nil {
		contract["approach"] = map[string]string{"id": a.ID, "strategy": a.Title}
	}
	if t != nil {
		contract["task"] = struct {
			ID, Title, Prompt, Current, Target, Sketch, Acceptance, Tests, Verify, CheckCommand, AcceptanceCommand string
			PriorAttempt                                                                                           int
		}{t.ID, t.Title, t.Prompt, t.Current, t.Target, t.Sketch, t.Acceptance, t.Tests, t.Verify, t.CheckCommand, t.AcceptanceCommand, t.Attempts}
	}
	if revisions {
		events, e := scopeRevisions(s, kind, id)
		if e != nil {
			return "", "", e
		}
		ids := make([]string, 0, len(events))
		for _, r := range events {
			ids = append(ids, r.ID)
		}
		contract["revision_ids"] = ids
	}
	raw, err := json.Marshal(contract)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), string(raw), nil
}

// RevisionContract applies the caller's existing GraphQL source/row budget inside
// the same transaction, before loading authored strings or raw revision events.
func RevisionContract(ctx context.Context, s *store.Store, kind, id string, rows, bytes int) (digest, brief string, pending []Revision, err error) {
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := CheckProject(tx, kind, id, ProjectRoot(ctx)); e != nil {
			return e
		}
		pattern := "(p:Plan)"
		node := "p"
		fields := []string{"p.id", "p.project_root", "p.title", "p.goal"}
		if kind == "approach" || kind == "task" {
			pattern += "-[:HAS_APPROACH]->(a:Approach)"
			node = "a"
			fields = append(fields, "a.id", "a.title")
		}
		if kind == "task" {
			pattern += "-[:HAS_TASK]->(t:Task)"
			node = "t"
			for _, f := range []string{"id", "title", "prompt", "current", "target", "sketch", "acceptance", "tests", "verify", "check_command", "acceptance_command"} {
				fields = append(fields, "t."+f)
			}
		}
		sizes := []string{}
		for _, f := range fields {
			sizes = append(sizes, "coalesce(size("+f+"),0)*4")
		}
		q := "MATCH " + pattern + " WHERE " + node + ".id=$id RETURN " + strings.Join(sizes, "+") + " AS bytes,p.id AS plan"
		if kind != "plan" {
			q += ",a.id AS approach"
		}
		rr, e := tx.Query(q, map[string]any{"id": id})
		if e != nil {
			return e
		}
		if len(rr) != 1 {
			return ErrNotFound
		}
		used, _ := rr[0]["bytes"].(int64)
		keys := []string{RevisionKeyPrefix + "plan/" + stringField(rr[0], "plan")}
		if kind != "plan" {
			keys = append(keys, RevisionKeyPrefix+"approach/"+stringField(rr[0], "approach"))
		}
		if kind == "task" {
			keys = append(keys, RevisionKeyPrefix+"task/"+id)
		}
		er, e := tx.Query(`MATCH(m:MemoryEntry) WHERE m.kind='event' AND m.key IN $keys RETURN count(m) AS rows,sum(coalesce(size(m.body),0)*4) AS bytes`, map[string]any{"keys": keys})
		if e != nil {
			return e
		}
		if len(er) > 0 {
			n, _ := er[0]["rows"].(int64)
			b, _ := er[0]["bytes"].(int64)
			used += b
			if rows > 0 && n+1 > int64(rows) {
				return fmt.Errorf("revision contract exceeds GraphQL row budget")
			}
		}
		if bytes > 0 && used > int64(bytes) {
			return fmt.Errorf("revision contract exceeds GraphQL source byte budget")
		}
		digest, brief, e = contractSnapshot(tx, kind, id, true)
		if e != nil {
			return e
		}
		pending, e = pendingRevisions(tx, kind, id)
		return e
	})
	return
}
