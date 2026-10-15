package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"sort"
	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
	"strings"
	"time"
	"unicode/utf8"
)

// RevisionKeyPrefix is reserved to the owning revision API. Public authored
// knowledge may not create/supersede these existing MemoryEntry event rows.
const RevisionKeyPrefix = "_harness/revision/"

type Revision struct {
	ID, Kind, TargetKind, TargetID, Before, Source, SessionRef, Text, CreatedAt string
	Resolves                                                                    string `json:",omitempty"`
	TaskIDs                                                                     []string
}

func revisionEvents(s stateStore, kind, id string) ([]Revision, error) {
	rows, e := s.Query(`MATCH(m:MemoryEntry) WHERE m.kind='event' AND m.key=$key RETURN m.id AS id,m.body AS body ORDER BY m.created_at,m.id`, map[string]any{"key": RevisionKeyPrefix + kind + "/" + id})
	if e != nil {
		return nil, e
	}
	out := make([]Revision, 0, len(rows))
	for _, row := range rows {
		var r Revision
		if e = json.Unmarshal([]byte(stringField(row, "body")), &r); e != nil {
			return nil, e
		}
		if r.ID != stringField(row, "id") || r.TargetKind != kind || r.TargetID != id {
			return nil, fmt.Errorf("invalid owning revision receipt")
		}
		out = append(out, r)
	}
	return out, nil
}
func scopeRevisions(s stateStore, kind, id string) ([]Revision, error) {
	targets := [][2]string{{kind, id}}
	if kind == "task" {
		a, e := taskApproach(s, id)
		if e != nil {
			return nil, e
		}
		targets = append(targets, [2]string{"approach", a.ID})
		p, e := ApproachPlan(s, a.ID)
		if e != nil {
			return nil, e
		}
		targets = append(targets, [2]string{"plan", p.ID})
	}
	if kind == "approach" {
		p, e := ApproachPlan(s, id)
		if e != nil {
			return nil, e
		}
		targets = append(targets, [2]string{"plan", p.ID})
	}
	var out []Revision
	for _, target := range targets {
		rs, e := revisionEvents(s, target[0], target[1])
		if e != nil {
			return nil, e
		}
		out = append(out, rs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func pendingRevisions(s stateStore, kind, id string) ([]Revision, error) {
	rs, e := scopeRevisions(s, kind, id)
	if e != nil {
		return nil, e
	}
	resolved := map[string]bool{}
	for _, r := range rs {
		if r.Kind == "revision_resolved" {
			resolved[r.Resolves] = true
		}
	}
	var out []Revision
	for _, r := range rs {
		if r.Kind == "revision_request" && !resolved[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// PendingRevisions returns actual scope/ancestor requests, not inferred comment intent.
func PendingRevisions(ctx context.Context, s *store.Store, kind, id string) (out []Revision, err error) {
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := CheckProject(tx, kind, id, ProjectRoot(ctx)); e != nil {
			return e
		}
		out, err = pendingRevisions(tx, kind, id)
		return err
	})
	return
}
func requireNoRevision(s stateStore, kind, id string) error {
	rs, e := pendingRevisions(s, kind, id)
	if e != nil {
		return e
	}
	if len(rs) > 0 {
		return fmt.Errorf("%s %s has pending explicit revision %s; pause, reconcile and recheck readiness", kind, id, rs[0].ID)
	}
	return nil
}
func revisionTasks(s stateStore, kind, id string) ([]*Task, error) {
	switch kind {
	case "task":
		t, e := GetTask(s, id)
		if e != nil {
			return nil, e
		}
		return []*Task{t}, nil
	case "approach":
		return ApproachTasks(s, id)
	case "plan":
		as, e := PlanApproaches(s, id)
		if e != nil {
			return nil, e
		}
		var ts []*Task
		for _, a := range as {
			items, e := ApproachTasks(s, a.ID)
			if e != nil {
				return nil, e
			}
			ts = append(ts, items...)
		}
		return ts, nil
	}
	return nil, fmt.Errorf("unknown revision kind %q", kind)
}
func appendRevision(s stateStore, r Revision, root string) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return s.Write(`CREATE(m:MemoryEntry {id:$id,kind:'event',body:$body,created_at:$now,session_ref:$session,project_root:$root,key:$key})`, map[string]any{"id": r.ID, "body": string(raw), "now": time.Now().UTC(), "session": r.SessionRef, "root": root, "key": RevisionKeyPrefix + r.TargetKind + "/" + r.TargetID})
}
func appendTaskRevision(s stateStore, t *Task, r Revision) error {
	log, e := outcome.AppendRecord(t.AttemptLog, struct {
		Kind, ID string
		Attempt  int
		Revision Revision
	}{r.Kind, r.ID, t.Attempts, r})
	if e != nil {
		return e
	}
	return s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log,t.check_projection=$projection,t.verification=$verification,t.verification_reason=$reason`, map[string]any{"id": t.ID, "log": log, "projection": outcome.CheckProjection(log), "verification": string(outcome.Unverified), "reason": "explicit revision requires a fresh checked attempt"})
}

// Every revision mutation shares this owning project/snapshot admission.
func checkRevisionContract(ctx context.Context, s stateStore, kind, id, expected string) error {
	if ProjectRoot(ctx) == "" {
		return fmt.Errorf("revision needs captured project")
	}
	if e := CheckProject(s, kind, id, ProjectRoot(ctx)); e != nil {
		return e
	}
	p, a, _, e := contractAncestry(s, kind, id)
	if e != nil {
		return e
	}
	if p.State == PlanRetired || a != nil && a.State == ApproachRetired {
		return fmt.Errorf("retired work cannot be revised")
	}
	digest, _, e := contractSnapshot(s, kind, id, true)
	if e != nil {
		return e
	}
	if expected == "" || digest != expected {
		return fmt.Errorf("contract changed before revision; read it again")
	}
	return nil
}
func publishRevision(s stateStore, root string, r Revision, tasks []*Task) error {
	if e := appendRevision(s, r, root); e != nil {
		return e
	}
	for _, t := range tasks {
		if e := appendTaskRevision(s, t, r); e != nil {
			return e
		}
	}
	return nil
}

// RequestRevision commits an explicit authored request and evidence barriers together.
// It does not interrupt an in-flight primitive, alter authored fields, or grant a retry.
func RequestRevision(ctx context.Context, s *store.Store, kind, id, expected, source, sessionRef, text string) (out *Revision, err error) {
	if ProjectRoot(ctx) == "" || strings.TrimSpace(text) == "" || !utf8.ValidString(text) || (source != "user" && source != "model-authored" && source != "api-authored") {
		return nil, fmt.Errorf("revision needs captured project, append-site source and nonblank UTF-8 text")
	}
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := checkRevisionContract(ctx, tx, kind, id, expected); e != nil {
			return e
		}
		if e := requireNoRevision(tx, kind, id); e != nil {
			return e
		}
		before, _, e := contractSnapshot(tx, kind, id, false)
		if e != nil {
			return e
		}
		tasks, e := revisionTasks(tx, kind, id)
		if e != nil {
			return e
		}
		for _, t := range tasks {
			if e = requireNoRevision(tx, "task", t.ID); e != nil {
				return e
			}
		}
		r := Revision{ID: uuid.NewString(), Kind: "revision_request", TargetKind: kind, TargetID: id, Before: before, Source: source, SessionRef: sessionRef, Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		for _, t := range tasks {
			r.TaskIDs = append(r.TaskIDs, t.ID)
		}
		sort.Strings(r.TaskIDs)
		if e = publishRevision(tx, ProjectRoot(ctx), r, tasks); e != nil {
			return e
		}
		out = &r
		return nil
	})
	return
}

// PausePendingRevision is called at the actual tool/check/review boundary. Files
// and attempt counts are untouched; already stopped work receives no new budget.
func PausePendingRevision(ctx context.Context, s *store.Store, id string, attempt int) error {
	paused := ""
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := CheckProject(tx, "task", id, ProjectRoot(ctx)); e != nil {
			return e
		}
		rs, e := pendingRevisions(tx, "task", id)
		if e != nil || len(rs) == 0 {
			return e
		}
		paused = rs[0].ID
		t, e := GetTask(tx, id)
		if e != nil {
			return e
		}
		if t.Attempts != attempt {
			return fmt.Errorf("revision boundary has stale task attempt")
		}
		a, e := taskApproach(tx, id)
		if e != nil {
			return e
		}
		if t.State == TaskRunning {
			if e = setState(tx, "Task", id, TaskStopped); e != nil {
				return e
			}
		}
		if a.State != ApproachRetired {
			if e = setState(tx, "Approach", a.ID, ApproachPaused); e != nil {
				return e
			}
			if e = markEdited(tx, a.ID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if paused != "" {
		return fmt.Errorf("explicit revision %s paused task %s before the next action; reconcile and recheck Ready", paused, id)
	}
	return nil
}

// EditRevision changes one explicitly named authored field while all affected work
// is paused. It neither resolves the request nor calls Ready/launch.
func EditRevision(ctx context.Context, s *store.Store, kind, id, expected, field, value string) error {
	if ProjectRoot(ctx) == "" {
		return fmt.Errorf("revision edit needs captured project")
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("revision value must be UTF-8")
	}
	return s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := checkRevisionContract(ctx, tx, kind, id, expected); e != nil {
			return e
		}
		rs, e := pendingRevisions(tx, kind, id)
		if e != nil {
			return e
		}
		if len(rs) == 0 {
			return fmt.Errorf("no pending explicit revision")
		}
		tasks, e := revisionTasks(tx, kind, id)
		if e != nil {
			return e
		}
		for _, t := range tasks {
			if t.State == TaskRunning {
				return fmt.Errorf("task %s must reach revision pause boundary before edit", t.ID)
			}
		}
		allowed := map[string][]string{"plan": {"title", "goal"}, "approach": {"title"}, "task": {"title", "prompt", "current", "target", "sketch", "acceptance", "tests", "verify", "check_command", "acceptance_command"}}
		ok := false
		for _, f := range allowed[kind] {
			ok = ok || f == field
		}
		if !ok {
			return fmt.Errorf("unsupported %s revision field %q", kind, field)
		}
		if (field == "title" || field == "prompt" || field == "goal") && strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s remains a required nonblank authored field", field)
		}
		label := map[string]string{"plan": "Plan", "approach": "Approach", "task": "Task"}[kind]
		if e = tx.Write("MATCH(x:"+label+") WHERE x.id=$id SET x."+field+"=$value", map[string]any{"id": id, "value": value}); e != nil {
			return e
		}
		for _, t := range tasks {
			a, e := taskApproach(tx, t.ID)
			if e != nil {
				return e
			}
			p, e := ApproachPlan(tx, a.ID)
			if e != nil {
				return e
			}
			if p.State == PlanComplete {
				if e = setState(tx, "Plan", p.ID, PlanActive); e != nil {
					return e
				}
			}
			if a.State != ApproachRetired {
				if e = setState(tx, "Approach", a.ID, ApproachPaused); e != nil {
					return e
				}
				if e = markEdited(tx, a.ID); e != nil {
					return e
				}
			}
		}
		return nil
	})
}

// ResolveRevision records explicit reconciliation. Requeue, real Ready and a new
// admitted attempt remain separate operations; old check/review facts stay invalid.
func ResolveRevision(ctx context.Context, s *store.Store, kind, id, revisionID, expected string) (out *Revision, err error) {
	if ProjectRoot(ctx) == "" {
		return nil, fmt.Errorf("revision resolution needs captured project")
	}
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := checkRevisionContract(ctx, tx, kind, id, expected); e != nil {
			return e
		}
		rs, e := pendingRevisions(tx, kind, id)
		if e != nil {
			return e
		}
		var request *Revision
		for _, r := range rs {
			if r.ID == revisionID && r.TargetKind == kind && r.TargetID == id {
				copy := r
				request = &copy
			}
		}
		if request == nil {
			return fmt.Errorf("no matching pending scope revision")
		}
		authored, _, e := contractSnapshot(tx, kind, id, false)
		if e != nil {
			return e
		}
		if authored == request.Before {
			return fmt.Errorf("revision has no changed authored contract")
		}
		tasks, e := revisionTasks(tx, kind, id)
		if e != nil {
			return e
		}
		for _, t := range tasks {
			if t.State == TaskRunning {
				return fmt.Errorf("task %s has not paused for revision", t.ID)
			}
		}
		r := Revision{ID: uuid.NewString(), Kind: "revision_resolved", TargetKind: kind, TargetID: id, Resolves: revisionID, Before: authored, Source: request.Source, SessionRef: request.SessionRef, Text: "explicit authored reconciliation; readiness and fresh evidence still required", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskIDs: request.TaskIDs}
		if e = publishRevision(tx, ProjectRoot(ctx), r, tasks); e != nil {
			return e
		}
		out = &r
		return nil
	})
	return
}

// PendingWorkRevisions supports orchestrator readback over its actual descendants.
func PendingWorkRevisions(ctx context.Context, s *store.Store, kind, id string) (out []Revision, err error) {
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		if e := CheckProject(tx, kind, id, ProjectRoot(ctx)); e != nil {
			return e
		}
		out, err = pendingRevisions(tx, kind, id)
		if err != nil {
			return err
		}
		tasks, e := revisionTasks(tx, kind, id)
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, r := range out {
			seen[r.ID] = true
		}
		for _, t := range tasks {
			rs, e := pendingRevisions(tx, "task", t.ID)
			if e != nil {
				return e
			}
			for _, r := range rs {
				if !seen[r.ID] {
					out = append(out, r)
					seen[r.ID] = true
				}
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
		return nil
	})
	return
}

// RevisionReconciled permits an explicit requeue form, never an automatic retry.
func RevisionReconciled(t *Task) bool {
	var records []struct {
		Kind    string
		Attempt int
	}
	if json.Unmarshal([]byte(t.AttemptLog), &records) != nil {
		return false
	}
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind == "revision_request" || r.Kind == "revision_resolved" {
			return r.Kind == "revision_resolved" && r.Attempt == t.Attempts
		}
	}
	return false
}
