package run

import (
	"context"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestStoppedUnchangedTaskRetryPreservesBriefAndReadiness(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	aid := f.scope.ID
	f.call(tool.Args{"invoke": "COMPLETE"})
	ctx := plan.WithProject(context.Background(), f.d.Workspace)
	snapshot, err := plan.ReadinessSnapshot(f.d.Store, aid)
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.ClaimTaskContext(ctx, f.d.Store, id, snapshot, "true")
	if err != nil {
		t.Fatal(err)
	}
	task.Execution = outcome.Stopped
	task.Verification = outcome.Unverified
	task.ExecutionReason = "transport interrupted"
	if err = plan.SettleTaskOutcome(f.d.Store, task, plan.TaskStopped); err != nil {
		t.Fatal(err)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "task", ID: id}
	before, _ := plan.GetTask(f.d.Store, id)
	judgments := f.judgments
	f.call(tool.Args{"invoke": "STATUS"})
	after, _ := plan.GetTask(f.d.Store, id)
	if after.State != plan.TaskStopped || after.Attempts != before.Attempts {
		t.Fatal("STATUS retried task")
	}
	f.call(tool.Args{"invoke": "REQUEUE"})
	after, _ = plan.GetTask(f.d.Store, id)
	a, _ := plan.GetApproach(f.d.Store, aid)
	if !after.SameBrief(before) || after.AttemptLog != before.AttemptLog || after.Attempts != before.Attempts || a.NeedsReady || f.judgments != judgments {
		t.Fatalf("retry rewrote/rejudged work: %+v %+v", after, a)
	}
	d := f.definition()
	_, args, err := d.Bind(ctx, tool.Args{"invoke": "EXECUTE"})
	if err != nil || args["task"] != id {
		t.Fatalf("wrong resumed owner: %v %v", args, err)
	}
	// A new claim is still required; old checks never qualify its new attempt.
	snapshot, _ = plan.ReadinessSnapshot(f.d.Store, aid)
	claimed, err := plan.ClaimTaskContext(ctx, f.d.Store, id, snapshot, "true")
	if err != nil || claimed.Attempts != before.Attempts+1 || claimed.Qualified() {
		t.Fatalf("invalid new attempt: %+v %v", claimed, err)
	}
}

func TestSelectedPATRefusalNamesCurrentOwnerAndLegalHelp(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	err := planningProjectCoding(context.Background(), f.d, f.scope)
	if err == nil || !strings.Contains(err.Error(), f.scope.ID) || !strings.Contains(err.Error(), "observe") || !strings.Contains(err.Error(), `help(topic="plan")`) || strings.Contains(err.Error(), "all tasks") {
		t.Fatalf("generic/misleading recovery: %v", err)
	}
}
