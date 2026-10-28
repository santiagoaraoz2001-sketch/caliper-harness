package launch

import (
	"context"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"testing"
)

func TestAssignedUserFeedbackSurvivesSettlementWithoutAuthority(t *testing.T) {
	for _, state := range []string{plan.TaskStopped, plan.TaskDone, plan.TaskFailed, plan.TaskCancelled} {
		t.Run(state, func(t *testing.T) { assignedFeedbackSettlement(t, state) })
	}
}

func assignedFeedbackSettlement(t *testing.T, state string) {
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) { return "PASS: fixture", nil }))
	a := &Assigned{Deps: d, Root: root}
	identity, _, err := a.Start(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AppendComment(t.Context(), "during work", "user-session"); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	task.Execution = outcome.Stopped
	if state == plan.TaskDone {
		task.Execution = outcome.Completed
	}
	if state == plan.TaskFailed {
		task.Execution = outcome.Failed
	}
	if state == plan.TaskCancelled {
		task.Execution = outcome.Cancelled
	}
	task.Verification = outcome.Unverified
	if err = plan.SettleTaskOutcome(d.Store, task, state); err != nil {
		t.Fatal(err)
	}
	a.settled = true
	before, _ := plan.GetTask(d.Store, ids[0])
	if err = a.AppendComment(t.Context(), "late α repair request", "user-session"); err != nil {
		t.Fatal(err)
	}
	after, _ := plan.GetTask(d.Store, ids[0])
	comments, err := outcome.TaskComments(after.AttemptLog, identity.Attempt)
	if err != nil || len(comments) != 2 || comments[1].Text != "late α repair request" || after.State != before.State || !after.SameBrief(before) || after.Verification != before.Verification {
		t.Fatalf("feedback changed work or was lost: %+v %v", comments, err)
	}
	if err = a.Admit(t.Context()); err == nil {
		t.Fatal("feedback granted execution authority")
	}
	identity.Attempt++
	if err = a.AppendComment(t.Context(), "stale attempt", "user-session"); err == nil {
		t.Fatal("stale identity accepted")
	}
	identity.Attempt--
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err = a.AppendComment(cancelled, "cancelled persistence", "user-session"); err == nil {
		t.Fatal("cancelled feedback acknowledged")
	}
	if err = d.Store.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempts=$attempt`, map[string]any{"id": ids[0], "attempt": identity.Attempt + 1}); err != nil {
		t.Fatal(err)
	}
	if err = a.AppendComment(t.Context(), "newer attempt owns task", "user-session"); err == nil {
		t.Fatal("old claimed attempt accepted after newer attempt")
	}
	a.Root = t.TempDir()
	if err = a.AppendComment(t.Context(), "foreign project", "user-session"); err == nil {
		t.Fatal("foreign identity accepted")
	}
	last, _ := plan.GetTask(d.Store, ids[0])
	if last.AttemptLog != after.AttemptLog {
		t.Fatal("refusal changed comment log")
	}
}
