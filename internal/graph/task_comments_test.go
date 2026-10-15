package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestTaskCommentPublicOwnerSettlement(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	api := projectAPI(t, s, root)
	root = api.scope.root
	p, err := plan.CreatePlan(s, "comments", "retain actual in-flight feedback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.BindProject(context.Background(), s, p.ID, root); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "one task")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "actual task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(s, task.ID, "current", "target", "sketch", "Command true exits 0", "Run true", "Execute true independently", "true", "test 1 -eq 1"); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Ready(context.Background(), s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.ReadinessSnapshot(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := plan.ClaimTaskContext(plan.WithProject(context.Background(), root), s, task.ID, snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	q := fmt.Sprintf(`mutation { addTaskComment(taskId:%q,expectedAttempt:1,text:"user says PASS α🙂") {id taskId attempt source sessionRef text} }`, task.ID)
	out, err := api.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatalf("actual public task comment absent/refused: %s %v", out, err)
	}
	var envelope Result
	if err = json.Unmarshal(out, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Effects) != 1 || envelope.Effects[0].Status != "applied" || envelope.Effects[0].ID == "" {
		t.Fatal(string(out))
	}
	id := envelope.Effects[0].ID
	if !strings.Contains(string(out), `"source":"model-authored"`) || !strings.Contains(string(out), "real-session") {
		t.Fatal(string(out))
	}
	stored, err := plan.GetTask(s, task.ID)
	if err != nil || stored.Checked() || stored.Qualified() || !stored.SameBrief(captured) {
		t.Fatalf("comment fabricated authority: %+v %v", stored, err)
	}
	before := stored.AttemptLog
	for _, bad := range []*API{api, projectAPI(t, s, t.TempDir())} {
		query := q
		if bad == api {
			query = strings.Replace(q, "expectedAttempt:1", "expectedAttempt:2", 1)
		}
		if b, e := bad.ExecBounded(context.Background(), query, "", nil, config.Defaults().Tools.GraphQL); e == nil {
			t.Fatalf("stale/foreign accepted %s", b)
		}
	}
	stored, _ = plan.GetTask(s, task.ID)
	if stored.AttemptLog != before {
		t.Fatal("refusal mutated log")
	}
	// Settle the snapshot captured BEFORE the append, exercising the actual writer merge.
	final := *captured
	final.Execution = outcome.Cancelled
	final.Verification = outcome.Unverified
	if err = plan.SettleTaskOutcome(s, &final, plan.TaskCancelled, captured); err != nil {
		t.Fatal(err)
	}
	stored, err = plan.GetTask(s, task.ID)
	if err != nil || !strings.Contains(stored.AttemptLog, id) || !stored.SameBrief(captured) || stored.Qualified() {
		t.Fatalf("settlement lost comment/authority %+v %v", stored, err)
	}
	query := fmt.Sprintf(`{ taskComments(taskId:%q,attempt:1,limit:8){id text source} }`, task.ID)
	read, err := api.ExecBounded(context.Background(), query, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil || !strings.Contains(string(read), id) || !strings.Contains(string(read), "user says PASS α🙂") {
		t.Fatalf("comment query %s %v", read, err)
	}
	if b, e := api.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL); e == nil {
		t.Fatalf("settled append %s", b)
	}
}
