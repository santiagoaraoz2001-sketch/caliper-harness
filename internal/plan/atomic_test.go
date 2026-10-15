package plan

import (
	"context"
	"reflect"
	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
	"testing"
)

func atomicFixture(t *testing.T) (*store.Store, *Plan, *Approach, *Task) {
	t.Helper()
	s := open(t)
	p, e := CreatePlan(s, "atomic", "goal")
	if e != nil {
		t.Fatal(e)
	}
	a, e := CreateApproach(s, p.ID, "a")
	if e != nil {
		t.Fatal(e)
	}
	x, e := CreateTask(s, a.ID, "t", "prompt")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = SetTaskBlock(s, x.ID, "c", "t", "s", "1", "tests", "verify", "true", "test 1 -eq 1"); e != nil {
		t.Fatal(e)
	}
	return s, p, a, x
}
func TestAtomicReworkPreservesAndReopens(t *testing.T) {
	s, p, a, x := atomicFixture(t)
	if _, e := fixtureApproachTransition(s, a.ID, ApproachReady); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionApproach(s, a.ID, ApproachRunning); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionTask(s, x.ID, TaskRunning); e != nil {
		t.Fatal(e)
	}
	x, _ = GetTask(s, x.ID)
	x.Execution = outcome.Completed
	x.Verification = outcome.Passed // synthetic paired lifecycle receipts, not execution proof
	x.Result = "raw café 😀"
	x.Review = "PASS prior"
	x.Attempts = 7
	x.AttemptLog = pairedFixtureLog(t, x)
	retainedLog := x.AttemptLog
	if e := SetTaskOutcome(s, x); e != nil {
		t.Fatal(e)
	}
	if e := SetTaskAttempts(s, x.ID, 7); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionTask(s, x.ID, TaskDone); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionApproach(s, a.ID, ApproachComplete); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionPlan(s, p.ID, PlanActive); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionPlan(s, p.ID, PlanComplete); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionTask(s, x.ID, TaskFailed); e != nil {
		t.Fatal(e)
	}
	p, _ = GetPlan(s, p.ID)
	a, _ = GetApproach(s, a.ID)
	x, _ = GetTask(s, x.ID)
	if p.State != PlanActive || a.State != ApproachPaused || x.State != TaskFailed || x.Verification != outcome.Unverified || x.Result != "raw café 😀" || x.Review != "PASS prior" || x.Attempts != 7 || x.AttemptLog != retainedLog {
		t.Fatalf("atomic explicit rework wrong p%+v a%+v task%+v", p, a, x)
	}
}
func TestAtomicBriefFreezeAndPausedReadiness(t *testing.T) {
	s, _, a, x := atomicFixture(t)
	if _, e := fixtureApproachTransition(s, a.ID, ApproachReady); e != nil {
		t.Fatal(e)
	}
	if _, e := SetTaskBlock(s, x.ID, "changed", "t", "s", "1", "tests", "verify"); e == nil {
		t.Error("ready brief edited")
	}
	if _, e := CreateTask(s, a.ID, "late", "late"); e == nil {
		t.Error("ready task set changed")
	}
	if _, e := TransitionApproach(s, a.ID, ApproachRunning); e != nil {
		t.Fatal(e)
	}
	if _, e := SetTaskBlock(s, x.ID, "changed", "t", "s", "1", "tests", "verify"); e == nil {
		t.Error("running brief edited")
	}
	if _, e := TransitionApproach(s, a.ID, ApproachPaused); e != nil {
		t.Fatal(e)
	}
	if _, e := SetTaskBlock(s, x.ID, "changed", "t", "s", "1", "tests", "verify"); e != nil {
		t.Fatal(e)
	}
	a, _ = GetApproach(s, a.ID)
	v := reflect.ValueOf(a).Elem().FieldByName("NeedsReady")
	if !v.IsValid() || !v.Bool() {
		t.Error("paused edit lacks readiness marker")
	}
	if _, e := TransitionApproach(s, a.ID, ApproachRunning); e == nil {
		t.Error("edited paused work resumes without new readiness")
	}
	if _, e := fixtureApproachTransition(s, a.ID, ApproachReady); e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionApproach(s, a.ID, ApproachRunning); e != nil {
		t.Fatal(e)
	}
}
func TestAtomicNonemptyPlanCompletion(t *testing.T) {
	s := open(t)
	p, e := CreatePlan(s, "empty", "goal")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = TransitionPlan(s, p.ID, PlanActive); e != nil {
		t.Fatal(e)
	}
	if _, e = TransitionPlan(s, p.ID, PlanComplete); e == nil {
		t.Error("empty Plan qualifies completion")
	}
}

func TestAtomicCompetingTransitionAndStaleReadyCheck(t *testing.T) {
	s, _, a, _ := atomicFixture(t)
	if _, err := Ready(context.Background(), s, a.ID, func(_ context.Context, _ *Plan, _ []*Task) error {
		_, err := CreateTask(s, a.ID, "added during judge", "prompt")
		return err
	}); err == nil {
		t.Fatal("readiness qualified a different task set")
	}
	current, _ := GetApproach(s, a.ID)
	if current.State != ApproachDraft {
		t.Fatalf("stale ready mutation %s", current.State)
	}
	s, _, a, _ = atomicFixture(t)
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { <-start; _, err := TransitionApproach(s, a.ID, ApproachRunning); results <- err }()
	}
	close(start)
	wins := 0
	for i := 0; i < 8; i++ {
		if <-results == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("ready→running winners %d/8", wins)
	}
}

func TestAtomicRetiredAncestorsRefuseWithoutMutation(t *testing.T) {
	for _, ancestor := range []string{"Plan", "Approach"} {
		t.Run(ancestor, func(t *testing.T) {
			s, p, a, x := atomicFixture(t)
			if err := s.Write(`MATCH(p:Plan)-[:HAS_APPROACH]->(a:Approach)-[:HAS_TASK]->(x:Task) WHERE x.id=$id SET p.state='complete',a.state='complete',x.state='done',x.verification='passed',x.result='retained'`, map[string]any{"id": x.ID}); err != nil {
				t.Fatal(err)
			}
			id := p.ID
			if ancestor == "Approach" {
				id = a.ID
			}
			if err := s.Write(`MATCH(n:`+ancestor+`) WHERE n.id=$id SET n.state='retired'`, map[string]any{"id": id}); err != nil {
				t.Fatal(err)
			}
			before, _ := GetTask(s, x.ID)
			beforePlan, _ := GetPlan(s, p.ID)
			beforeApproach, _ := GetApproach(s, a.ID)
			if _, err := TransitionTask(s, x.ID, TaskFailed); err == nil {
				t.Error("retired rework accepted")
			}
			if _, err := SetTaskBlock(s, x.ID, "changed", "t", "s", "1", "tests", "verify"); err == nil {
				t.Error("closed block accepted")
			}
			if _, err := CreateTask(s, a.ID, "late", "prompt"); err == nil {
				t.Error("closed task accepted")
			}
			if err := SetTaskResult(s, x.ID, "lost"); err == nil {
				t.Error("closed artifact accepted")
			}
			after, _ := GetTask(s, x.ID)
			afterPlan, _ := GetPlan(s, p.ID)
			afterApproach, _ := GetApproach(s, a.ID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforePlan, afterPlan) || !reflect.DeepEqual(beforeApproach, afterApproach) {
				t.Fatal("refusal changed retained hierarchy")
			}
		})
	}
}

func TestAtomicSettlementRollbackAndFreshReviewFailure(t *testing.T) {
	s, _, a, x := atomicFixture(t)
	x.Result = "must roll back"
	x.Execution = outcome.Completed
	x.Verification = outcome.Passed
	if err := SettleTaskOutcome(s, x, TaskDone); err == nil {
		t.Fatal("queued→done settlement accepted")
	}
	current, _ := GetTask(s, x.ID)
	if current.State != TaskQueued || current.Result != "" || current.Execution != outcome.Unknown {
		t.Fatalf("refused settlement leaked %+v", current)
	}
	for _, to := range []string{ApproachReady, ApproachRunning} {
		if _, err := fixtureApproachTransition(s, a.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := TransitionTask(s, x.ID, TaskRunning); err != nil {
		t.Fatal(err)
	}
	x.Result = "worker artifact"
	x.Verification = outcome.Unverified
	if err := SettleTaskOutcome(s, x, TaskDone); err != nil {
		t.Fatal(err)
	}
	x.Review = "FAIL real check"
	x.Verification = outcome.Rejected
	x.AttemptLog = `[{"Kind":"review","Output":"FAIL real check"}]`
	if err := SettleTaskOutcome(s, x, TaskFailed); err != nil {
		t.Fatal(err)
	}
	current, _ = GetTask(s, x.ID)
	if current.Verification != outcome.Rejected || current.Review != x.Review || current.Result != x.Result || current.AttemptLog != x.AttemptLog || current.State != TaskFailed {
		t.Fatalf("fresh FAIL lost %+v", current)
	}
}

func TestAtomicReadinessFailureAndUnchangedPause(t *testing.T) {
	s, _, a, x := atomicFixture(t)
	for _, to := range []string{ApproachReady, ApproachRunning, ApproachPaused} {
		if _, err := fixtureApproachTransition(s, a.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SetTaskBlock(s, x.ID, "c", "t", "s", "1", "tests", "verify"); err != nil {
		t.Fatal(err)
	}
	a, _ = GetApproach(s, a.ID)
	if a.NeedsReady {
		t.Fatal("identical brief invalidated readiness")
	}
	if _, err := TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatal("unchanged pause cannot resume", err)
	}
	if _, err := TransitionApproach(s, a.ID, ApproachPaused); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTask(s, a.ID, "new incomplete", "prompt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err == nil {
		t.Fatal("incomplete paused creation passed readiness")
	}
	a, _ = GetApproach(s, a.ID)
	if a.State != ApproachPaused || !a.NeedsReady {
		t.Fatalf("failed check cleared dirty flag %+v", a)
	}
}

func TestAtomicDirtyPauseCannotStartTaskDirectly(t *testing.T) {
	s, _, a, _ := atomicFixture(t)
	for _, to := range []string{ApproachReady, ApproachRunning, ApproachPaused} {
		if _, err := fixtureApproachTransition(s, a.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	task, err := CreateTask(s, a.ID, "new dirty task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionTask(s, task.ID, TaskRunning); err == nil {
		t.Fatal("direct owning task transition bypassed paused readiness")
	}
	task, _ = GetTask(s, task.ID)
	if task.State != TaskQueued {
		t.Fatalf("refusal changed task %+v", task)
	}
}
