package launch

import (
	"context"
	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
	"testing"
)

func TestReadyJudgmentBoundToExactBrief(t *testing.T) {
	s, aid, ids := setup(t, 1)
	m := &outcomeModel{reply: "must not run"}
	d := depsFor(s, m)
	d.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		for _, to := range []string{plan.ApproachRunning, plan.ApproachPaused} {
			if _, e := fixtureApproachTransition(s, aid, to); e != nil {
				return decision.Verdict{}, e
			}
		}
		if _, e := plan.SetTaskBlock(s, ids[0], "changed during judge", "after", "mech", "exit code is 0", "TestX", "run it"); e != nil {
			return decision.Verdict{}, e
		}
		if _, e := fixtureApproachTransition(s, aid, plan.ApproachReady); e != nil {
			return decision.Verdict{}, e
		}
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	res, err := New(d).Execute(context.Background(), tool.Args{"approach": aid})
	if err == nil || res.Workflow == nil || res.Workflow.Qualified {
		t.Fatalf("different brief admitted res%+v err%v", res, err)
	}
	x, _ := plan.GetTask(s, ids[0])
	if x.Attempts != 0 || x.State != plan.TaskQueued || x.Result != "" {
		t.Fatalf("worker admitted after stale judgment %+v", x)
	}
}

func TestReadyJudgmentBoundThroughUserApproval(t *testing.T) {
	s, aid, ids := setup(t, 1)
	m := &outcomeModel{reply: "must not run"}
	d := depsFor(s, m)
	d.AuthMode = "authorize-all"
	d.Approve = func(ctx context.Context, prompt string) bool {
		for _, to := range []string{plan.ApproachRunning, plan.ApproachPaused} {
			if _, e := fixtureApproachTransition(s, aid, to); e != nil {
				t.Error(e)
				return false
			}
		}
		if _, e := plan.SetTaskBlock(s, ids[0], "changed during approval", "after", "mech", "exit code is 0", "TestX", "run it"); e != nil {
			t.Error(e)
			return false
		}
		if _, e := fixtureApproachTransition(s, aid, plan.ApproachReady); e != nil {
			t.Error(e)
			return false
		}
		return true
	}
	res, err := New(d).Execute(context.Background(), tool.Args{"approach": aid})
	if err == nil || res.Workflow == nil || res.Workflow.Qualified || m.workers != 0 {
		t.Fatalf("stale approval launched res%+v err%v workers%d", res, err, m.workers)
	}
	x, _ := plan.GetTask(s, ids[0])
	a, _ := plan.GetApproach(s, aid)
	if x.Attempts != 0 || x.State != plan.TaskQueued || x.Result != "" || a.State != plan.ApproachReady {
		t.Fatalf("admission refusal mutated task%+v approach%+v", x, a)
	}
}
