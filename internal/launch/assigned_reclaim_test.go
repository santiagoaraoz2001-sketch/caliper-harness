package launch

import (
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
)

// A recorded stopped attempt that has explicitly been requeued is no longer
// a Running reclaim: task_start must take the ordinary fresh-claim route and
// admit the next attempt, not deadlock on the stale restored assignment.
func TestAssignedReclaimAfterRequeueStartsFreshAttempt(t *testing.T) {
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) {
		return "unused", nil
	}))
	first := &Assigned{Deps: d, Root: root}
	identity, _, err := first.Start(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(d.Store, ids[0], plan.TaskStopped); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(d.Store, ids[0], plan.TaskQueued); err != nil {
		t.Fatal(err)
	}

	resumed := &Assigned{Deps: d, Root: root}
	got, _, err := resumed.Reclaim(t.Context(), ids[0], identity.Attempt)
	if err != nil {
		t.Fatalf("requeued stopped attempt could not re-enter: %v", err)
	}
	if got.Attempt != identity.Attempt+1 {
		t.Fatalf("requeue did not start a fresh attempt: got %d want %d", got.Attempt, identity.Attempt+1)
	}
	current, err := plan.GetTask(d.Store, ids[0])
	if err != nil || current.State != plan.TaskRunning || current.Attempts != got.Attempt {
		t.Fatalf("fresh attempt not running: %+v %v", current, err)
	}
}
