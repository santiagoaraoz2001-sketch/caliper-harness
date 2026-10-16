package run

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// A running owner must expose completion guidance without pretending to be
// stopped, retrying the task, or qualifying it from a STATUS call.
func TestRunningPlanningStatusExplainsReviewHandoff(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := f.scope.ID
	node, _ := launch.ScopeNode(f.d.Store, f.scope)
	if _, err := plan.SetTaskBlock(f.d.Store, id, "observed", "target", "steps", "acceptance", "tests", "review", "true", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Ready(context.Background(), f.d.Store, node.Parent, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionApproach(f.d.Store, node.Parent, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(f.d.Store, id, plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	st, err := planningRead(f.d, f.scope)
	if err != nil || st.Phase != "running" {
		t.Fatalf("running task mislabeled: %s %v", st.Phase, err)
	}
	res := f.call(tool.Args{"invoke": "STATUS"})
	for _, want := range []string{"normal final answer", "independent review", "no tool call"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("STATUS lacks %q: %s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "answer only the next") || strings.Contains(res.Text, "running, stopped or cancelled") {
		t.Fatal("contradictory completion receipt", res.Text)
	}
	after, _ := plan.GetTask(f.d.Store, id)
	if after.State != plan.TaskRunning || after.Qualified() {
		t.Fatal("STATUS changed execution authority", after)
	}
	if _, err := plan.TransitionTask(f.d.Store, id, plan.TaskStopped); err != nil {
		t.Fatal(err)
	}
	st, err = planningRead(f.d, f.scope)
	if err != nil || st.Phase != "stopped" || strings.Contains(f.definition().Purpose, "normal final answer") {
		t.Fatal("stopped task offered active completion", st, err)
	}
}
