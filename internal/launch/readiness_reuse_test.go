package launch

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
)

// An admitted brief is judged once. A second readiness check of the same
// unchanged judged brief must reuse the admission, even when no human
// prompt is available for a fresh unsure verdict.
func TestReadyCheckReusesAdmittedBrief(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	calls := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: servesGoal, Confidence: 0.9}, nil
	}
	d.In = strings.NewReader("")
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("second check must reuse admission: %v", err)
	}
	if calls != 1 {
		t.Fatalf("judge calls = %d, want 1 (unchanged brief judged once)", calls)
	}
}

// An asked-admitted brief (unsure verdict approved by the user) is also
// reused: the identical brief must not ask the human a second time.
func TestReadyCheckReusesAskedAdmittedBrief(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	calls := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: servesGoal, Confidence: 0.5}, nil
	}
	d.In = strings.NewReader("y\n")
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("asked-admitted check: %v", err)
	}
	d.In, d.Out, d.Ask, d.ConfirmReadiness = nil, nil, nil, nil
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("second check must reuse asked admission without prompting: %v", err)
	}
	if calls != 1 {
		t.Fatalf("judge calls = %d, want 1", calls)
	}
}

// A pause deliberately re-gates admission: identical brief or not, the
// next readiness check judges again.
func TestReadyCheckPauseRejudgesAdmittedBrief(t *testing.T) {
	s, aid, d := gateDeps(t, 2)
	calls := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: servesGoal, Confidence: 0.9}, nil
	}
	d.In = strings.NewReader("")
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Resume goes through Ready (the paused approach is not launchable
	// directly); readiness must judge again rather than reuse.
	if _, err := Ready(context.Background(), d, aid); err != nil {
		t.Fatalf("post-pause ready: %v", err)
	}
	if calls != 2 {
		t.Fatalf("judge calls = %d, want 2 after pause", calls)
	}
}

// Changing any judged brief field invalidates reuse.
func TestReadyCheckChangedBriefRejudges(t *testing.T) {
	s, aid, d := gateDeps(t, 2)
	calls := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: servesGoal, Confidence: 0.9}, nil
	}
	d.In = strings.NewReader("")
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("first check: %v", err)
	}
	tasks, err := plan.ApproachTasks(s, aid)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks: %v", err)
	}
	if err := s.Write(`MATCH (t:Task) WHERE t.id=$id SET t.target='changed-target'`, map[string]any{"id": tasks[0].ID}); err != nil {
		t.Fatalf("mutate brief: %v", err)
	}
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("changed-brief check: %v", err)
	}
	if calls != 2 {
		t.Fatalf("judge calls = %d, want 2 after brief change", calls)
	}
}
