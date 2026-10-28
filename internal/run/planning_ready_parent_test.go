package run

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// A direct readiness mutation can legitimately leave its parent Draft. Its
// next question must not advertise a claim that the owning guard will refuse.
func TestReadyQuestionTracksParentWithoutActivatingOrClaiming(t *testing.T) {
	f := newPlanningFixture(t)
	ctx := plan.WithProject(context.Background(), f.d.Workspace)
	p, err := plan.CreatePlanContext(ctx, f.d.Store, "Converter", "Convert units")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, f.d.Store, p.ID, "Complete converter")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTaskContext(ctx, f.d.Store, a.ID, "Converter", "Implement and verify conversion")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(f.d.Store, task.ID, "missing", "conversion works", "implement and test", "correct conversion", "conversion assertions", "independent check", "true", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Ready(ctx, f.d.Store, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: a.ID}
	inactive := f.definition()
	if len(inactive.Params) != 1 || len(inactive.Params[0].Enum) != 1 || inactive.Params[0].Enum[0] != "STATUS" {
		t.Fatal("inactive parent advertises an illegal claim", inactive.Schema())
	}
	if _, gap := tool.ValidateJSON(inactive, `{"invoke":"EXECUTE"}`); gap == "" {
		t.Fatal("EXECUTE accepted for Draft parent")
	}
	if !strings.Contains(inactive.Purpose, "report the result without tools and stop") {
		t.Fatal(inactive.Purpose)
	}
	f.call(tool.Args{"invoke": "STATUS"})
	p, err = plan.GetPlan(f.d.Store, p.ID)
	if err != nil || p.State != plan.PlanDraft {
		t.Fatalf("status activated parent: %+v %v", p, err)
	}
	task, err = plan.GetTask(f.d.Store, task.ID)
	if err != nil || task.State != plan.TaskQueued || task.Attempts != 0 {
		t.Fatalf("status claimed work: %+v %v", task, err)
	}
	if _, err = plan.TransitionPlanContext(ctx, f.d.Store, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	stale, staleArgs, err := inactive.Bind(ctx, tool.Args{"invoke": "STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stale.Execute(ctx, staleArgs); err == nil {
		t.Fatal("stale question accepted after parent activation")
	}
	active := f.definition()
	if len(active.Params[0].Enum) != 1 || active.Params[0].Enum[0] != "EXECUTE" {
		t.Fatal("authorized execution route lost", active.Schema())
	}
	bound, args, err := active.Bind(ctx, tool.Args{"invoke": "EXECUTE"})
	if err != nil || bound.Name != "task_start" || args["task"] != task.ID {
		t.Fatalf("claim route lost: %+v %+v %v", bound, args, err)
	}
}
