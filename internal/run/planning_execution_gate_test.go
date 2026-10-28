package run

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestManagedPlanningCodingRequiresClaim(t *testing.T) {
	f := newPlanningFixture(t)
	if err := planningProjectCoding(t.Context(), f.d, nil); err != nil {
		t.Fatal(err)
	}
	scope := &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "root"}
	if err := planningProjectCoding(t.Context(), f.d, scope); err == nil || !strings.Contains(err.Error(), "EXECUTE action claims a task") || !strings.Contains(err.Error(), `help(topic="plan")`) {
		t.Fatalf("root planning bypass: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := planningProjectCoding(ctx, f.d, nil); err != context.Canceled {
		t.Fatalf("cancelled ordinary admission: %v", err)
	}
}

func TestManagedReadyRefusesIncompleteSiblingBeforeClaim(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	first := f.scope.ID
	a, err := plan.TaskApproach(f.d.Store, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(f.d.Store, first, "observed", "target", "method", "assertion", "test", "review", "true", "true"); err != nil {
		t.Fatal(err)
	}
	sibling, err := plan.CreateTask(f.d.Store, a.ID, "second", "second outcome")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = launch.Ready(t.Context(), f.d, a.ID); err == nil || !strings.Contains(err.Error(), sibling.ID) {
		t.Fatalf("incomplete sibling readied: %v", err)
	}
	snapshot, err := plan.ReadinessSnapshot(f.d.Store, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.ClaimTaskContext(t.Context(), f.d.Store, first, snapshot, "true"); err == nil {
		t.Fatal("first task claimed before sibling drafted")
	}
	task, err := plan.GetTask(f.d.Store, first)
	if err != nil || task.Attempts != 0 || task.State != plan.TaskQueued {
		t.Fatalf("failed admission changed task: %+v %v", task, err)
	}
}

// Owning readiness cannot produce this state; historical/corrupt records must not
// advertise qualification merely because iteration over zero tasks succeeds.
func TestManagedEmptyAdmittedApproachDoesNotAdvertiseCompletion(t *testing.T) {
	for _, state := range []string{plan.ApproachReady, plan.ApproachRunning} {
		t.Run(state, func(t *testing.T) {
			f := newPlanningFixture(t)
			f.call(tool.Args{})
			f.call(tool.Args{"title": "plan", "outcome": "outcome", "rules": "rules", "articles": "article", "proof": "proof"})
			f.call(tool.Args{"strategy": "strategy"})
			if err := f.d.Store.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state=$state`, map[string]any{"id": f.scope.ID, "state": state}); err != nil {
				t.Fatal(err)
			}
			if _, err := planningDefinition(f.d, f.reg, f.scope); err == nil || !strings.Contains(err.Error(), "has no tasks") {
				t.Fatalf("empty admitted approach advertised completion: %v", err)
			}
		})
	}
}
