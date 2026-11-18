package plan

import (
	"context"
	"testing"
)

// A Running task whose executor ended without settlement can be re-admitted
// by proving the recorded attempt; wrong attempts and plain claims cannot.
func TestReclaimRunningAttempt(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "goal", "repair the requested behavior")
	a, _ := CreateApproach(s, p.ID, "repair")
	x, _ := CreateTask(s, a.ID, "task", "original requested behavior")
	if _, err := SetTaskBlock(s, x.ID, "observed", "correct", "repair", "check exit0", "tests", "review", "true"); err != nil {
		t.Fatal(err)
	}
	TransitionPlan(s, p.ID, PlanActive)
	if _, err := Ready(context.Background(), s, a.ID, func(context.Context, *Plan, []*Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadinessSnapshot(s, a.ID)
	if _, err := ReclaimTaskContext(context.Background(), s, x.ID, snapshot, "true", 1); err == nil {
		t.Fatal("reclaim admitted a task that was never claimed")
	}
	claimed, err := ClaimTaskContext(context.Background(), s, x.ID, snapshot, "true")
	if err != nil || claimed.Attempts != 1 || claimed.State != TaskRunning {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	snapshot, _ = ReadinessSnapshot(s, a.ID)
	if _, err := ClaimTaskContext(context.Background(), s, x.ID, snapshot, "true"); err == nil {
		t.Fatal("plain claim admitted a Running task")
	}
	if _, err := ReclaimTaskContext(context.Background(), s, x.ID, snapshot, "true", 2); err == nil {
		t.Fatal("reclaim admitted a different attempt")
	}
	reclaimed, err := ReclaimTaskContext(context.Background(), s, x.ID, snapshot, "true", 1)
	if err != nil || reclaimed.Attempts != 1 || reclaimed.State != TaskRunning {
		t.Fatalf("reclaim: %+v %v", reclaimed, err)
	}
}
