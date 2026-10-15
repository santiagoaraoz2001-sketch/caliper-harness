package plan

import (
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/outcome"
	"strings"
	"testing"
)

func TestRevisionEmptyParentsAndRevertedApproval(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	ctx := WithProject(context.Background(), root)
	p, e := CreatePlanContext(ctx, s, "p", "A")
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"plan", "approach"} {
		id, field := p.ID, "goal"
		if kind == "approach" {
			a, e := CreateApproachContext(ctx, s, p.ID, "A")
			if e != nil {
				t.Fatal(e)
			}
			id, field = a.ID, "title"
		}
		original, _, e := ContractSnapshot(ctx, s, kind, id)
		if e != nil {
			t.Fatal(e)
		}
		for _, value := range []string{"B", "A"} {
			before, _, e := ContractSnapshot(ctx, s, kind, id)
			if e != nil {
				t.Fatal(e)
			}
			r, e := RequestRevision(ctx, s, kind, id, before, "user", "session", "change explicitly")
			if e != nil {
				t.Fatal(e)
			}
			if len(r.TaskIDs) != 0 {
				t.Fatal("invented child work")
			}
			current, _, _ := ContractSnapshot(ctx, s, kind, id)
			if e = EditRevision(ctx, s, kind, id, current, field, value); e != nil {
				t.Fatal(e)
			}
			current, _, _ = ContractSnapshot(ctx, s, kind, id)
			if _, e = ResolveRevision(ctx, s, kind, id, r.ID, current); e != nil {
				t.Fatal(e)
			}
		}
		final, _, e := ContractSnapshot(ctx, s, kind, id)
		if e != nil || final == original {
			t.Fatal("reverted authored text resurrected approval", e)
		}
	}
}

func TestRevisionBoundaryArtifactsAndFreshAttempt(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	ctx := WithProject(context.Background(), root)
	p, e := CreatePlanContext(ctx, s, "p", "A")
	if e != nil {
		t.Fatal(e)
	}
	a, e := CreateApproachContext(ctx, s, p.ID, "strategy")
	if e != nil {
		t.Fatal(e)
	}
	task, e := CreateTaskContext(ctx, s, a.ID, "t", "objective")
	if e != nil {
		t.Fatal(e)
	}
	draftBlock(t, s, task.ID)
	if _, e = TransitionPlan(s, p.ID, PlanActive); e != nil {
		t.Fatal(e)
	}
	// Actual Ready owner, labelled mechanical checker double; no native semantic claim.
	ready := func() {
		t.Helper()
		if _, e = Ready(ctx, s, a.ID, func(context.Context, *Plan, []*Task) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	ready()
	digest, _, _ := ContractSnapshot(ctx, s, "task", task.ID)
	claimed, e := ClaimTaskContractContext(ctx, s, task.ID, digest, "true")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = AppendTaskComment(ctx, s, task.ID, 1, "user", "session", "change explicitly"); e != nil {
		t.Fatal(e)
	}
	if e = PausePendingRevision(ctx, s, task.ID, 1); e != nil {
		t.Fatal("guidance blocked", e)
	}
	marker := filepath.Join(root, "artifact")
	if e = os.WriteFile(marker, []byte("actual pre-revision file"), 0600); e != nil {
		t.Fatal(e)
	}
	before, _, _ := ContractSnapshot(ctx, s, "plan", p.ID)
	r, e := RequestRevision(ctx, s, "plan", p.ID, before, "user", "session", "change explicitly")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.TaskIDs) != 1 || r.TaskIDs[0] != task.ID {
		t.Fatal("wrong affected descendants", r)
	}
	if e = PausePendingRevision(ctx, s, task.ID, 1); e == nil || !strings.Contains(e.Error(), r.ID) {
		t.Fatal("next boundary admitted", e)
	}
	got, e := GetTask(s, task.ID)
	if e != nil || got.State != TaskStopped || got.Attempts != 1 {
		t.Fatal("pause changed attempt", got, e)
	}
	if b, e := os.ReadFile(marker); e != nil || string(b) != "actual pre-revision file" {
		t.Fatal("artifact lost", e)
	}
	stale := *claimed
	stale.AttemptLog = pairedFixtureLog(t, &stale)
	stale.Verification = outcome.Passed
	stale.Execution = outcome.Completed
	if e = SetTaskOutcome(s, &stale); e != nil {
		t.Fatal(e)
	}
	got, e = GetTask(s, task.ID)
	if e != nil || got.Checked() || got.Verification != outcome.Unverified || outcome.Checked(outcome.CheckProjection(got.AttemptLog), got.CheckCommand, 1, got.AcceptanceCommand) {
		t.Fatal("old stale pass survived revision", e)
	}
	if e = SettleTaskOutcome(s, &stale, TaskDone, claimed); e == nil {
		t.Fatal("old review settlement admitted")
	}
	current, _, _ := ContractSnapshot(ctx, s, "plan", p.ID)
	if e = EditRevision(ctx, s, "plan", p.ID, current, "goal", "B"); e != nil {
		t.Fatal(e)
	}
	current, _, _ = ContractSnapshot(ctx, s, "plan", p.ID)
	if _, e = ResolveRevision(ctx, s, "plan", p.ID, r.ID, current); e != nil {
		t.Fatal(e)
	}
	current, _, _ = ContractSnapshot(ctx, s, "task", task.ID)
	if _, e = ClaimTaskContractContext(ctx, s, task.ID, current, "true"); e == nil {
		t.Fatal("resolved revision bypassed Ready/requeue")
	}
	if _, e = TransitionTaskContext(ctx, s, task.ID, TaskQueued); e != nil {
		t.Fatal(e)
	}
	ready()
	current, _, _ = ContractSnapshot(ctx, s, "task", task.ID)
	fresh, e := ClaimTaskContractContext(ctx, s, task.ID, current, "true")
	if e != nil || fresh.Attempts != 2 || fresh.Checked() {
		t.Fatal("fresh readmission", fresh, e)
	}
	if _, _, e = ContractSnapshot(WithProject(ctx, t.TempDir()), s, "task", task.ID); e == nil {
		t.Fatal("foreign contract admitted")
	}
}

func TestRevisionSnapshotOwnContractsAndConcurrentSiblingStart(t *testing.T) {
	s := open(t)
	ctx := WithProject(context.Background(), t.TempDir())
	p, e := CreatePlanContext(ctx, s, "p", "goal")
	if e != nil {
		t.Fatal(e)
	}
	a, e := CreateApproachContext(ctx, s, p.ID, "strategy")
	if e != nil {
		t.Fatal(e)
	}
	pd, _, _ := ContractSnapshot(ctx, s, "plan", p.ID)
	ad, _, _ := ContractSnapshot(ctx, s, "approach", a.ID)
	first, e := CreateTaskContext(ctx, s, a.ID, "one", "first objective")
	if e != nil {
		t.Fatal(e)
	}
	draftBlock(t, s, first.ID)
	second, e := CreateTaskContext(ctx, s, a.ID, "two", "second objective")
	if e != nil {
		t.Fatal(e)
	}
	draftBlock(t, s, second.ID)
	for kind, want := range map[string]string{"plan": pd, "approach": ad} {
		id := p.ID
		if kind == "approach" {
			id = a.ID
		}
		got, _, e := ContractSnapshot(ctx, s, kind, id)
		if e != nil || got != want {
			t.Fatal("child drafts invalidated parent approval", kind, e)
		}
	}
	if _, e = TransitionPlan(s, p.ID, PlanActive); e != nil {
		t.Fatal(e)
	}
	if _, e = Ready(ctx, s, a.ID, func(context.Context, *Plan, []*Task) error { return nil }); e != nil {
		t.Fatal(e)
	}
	one, _, _ := ContractSnapshot(ctx, s, "task", first.ID)
	two, _, _ := ContractSnapshot(ctx, s, "task", second.ID)
	if _, e = ClaimTaskContractContext(ctx, s, first.ID, one, "true"); e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimTaskContractContext(ctx, s, second.ID, two, "true"); e != nil {
		t.Fatal("independent sibling start invalidated task contract", e)
	}
	pd, _, _ = ContractSnapshot(ctx, s, "plan", p.ID)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = RequestRevision(canceled, s, "plan", p.ID, pd, "user", "session", "cancelled"); e == nil {
		t.Fatal("cancelled request committed")
	}
	if rs, e := PendingRevisions(ctx, s, "plan", p.ID); e != nil || len(rs) != 0 {
		t.Fatal("cancelled event published", rs, e)
	}
	if _, e = RequestRevision(ctx, s, "plan", p.ID, "stale", "user", "session", "stale"); e == nil {
		t.Fatal("stale snapshot admitted")
	}
	r, e := RequestRevision(ctx, s, "plan", p.ID, pd, "user", "session", "change goal")
	if e != nil || len(r.TaskIDs) != 2 {
		t.Fatal("parent fanout missing", r, e)
	}
	current, _, _ := ContractSnapshot(ctx, s, "plan", p.ID)
	if e = EditRevision(ctx, s, "plan", p.ID, current, "goal", "new"); e == nil {
		t.Fatal("edited while workers still running")
	}
	for _, task := range []*Task{first, second} {
		if e = PausePendingRevision(ctx, s, task.ID, 1); e == nil {
			t.Fatal("pending sibling effect admitted")
		}
	}
	if e = EditRevision(ctx, s, "plan", p.ID, current, "goal", "new"); e != nil {
		t.Fatal(e)
	}
}
