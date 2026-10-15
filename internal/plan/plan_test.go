package plan

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
)

var errTestHook = errors.New("hook says no")

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPlanLifecycleChain(t *testing.T) {
	s := open(t)
	p, err := CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatalf("create plan: %s", err)
	}
	a, err := CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatalf("create approach: %s", err)
	}
	x, err := CreateTask(s, a.ID, "x", "do it")
	if err != nil {
		t.Fatalf("create task: %s", err)
	}
	draftBlock(t, s, x.ID)
	if p.State != PlanDraft || a.State != ApproachDraft || x.State != TaskQueued {
		t.Fatalf("bad initial states: %s %s %s", p.State, a.State, x.State)
	}

	moves := func(kind string) []string {
		t.Helper()
		var m []string
		var err error
		switch kind {
		case "plan":
			m, err = NextPlan(p.State)
		case "approach":
			m, err = NextApproach(a.State)
		case "task":
			m, err = NextTask(x.State)
		}
		if err != nil {
			t.Fatalf("next %s: %s", kind, err)
		}
		return m
	}
	if got := moves("plan"); len(got) != 1 || got[0] != PlanActive {
		t.Fatalf("plan next=%v", got)
	}

	if p, err = TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatalf("plan→active: %s", err)
	}
	if x, err = TransitionTask(s, x.ID, TaskRunning); err != nil {
		t.Fatalf("task→running: %s", err)
	}
	if x, err = TransitionTask(s, x.ID, TaskDone); err != nil {
		t.Fatalf("task→done: %s", err)
	}
	if a, err = fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatalf("approach→ready: %s", err)
	}
	if a, err = TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatalf("approach→running: %s", err)
	}
	recordCompleted(t, s, x.ID)
	if a, err = TransitionApproach(s, a.ID, ApproachComplete); err != nil {
		t.Fatalf("approach→complete: %s", err)
	}
	if p, err = TransitionPlan(s, p.ID, PlanComplete); err != nil {
		t.Fatalf("plan→complete: %s", err)
	}
	if p.State != PlanComplete || a.State != ApproachComplete || x.State != TaskDone {
		t.Fatalf("bad final states: %s %s %s", p.State, a.State, x.State)
	}
	// Done reopens to failed: the verification follow-up rejects bad
	// work back onto the queue instead of ending silent.
	if got := moves("task"); len(got) != 1 || got[0] != TaskFailed {
		t.Fatalf("done task next=%v, want [failed]", got)
	}
}

func TestPlanIllegalMoves(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	x, _ := CreateTask(s, a.ID, "x", "do it")

	cases := []struct {
		name string
		call func() error
		want string
	}{
		{"plan skips active", func() error { _, err := TransitionPlan(s, p.ID, PlanComplete); return err }, "legal moves: active"},
		{"approach skips ready", func() error { _, err := TransitionApproach(s, a.ID, ApproachRunning); return err }, "legal moves: ready"},
		{"task skips running", func() error { _, err := TransitionTask(s, x.ID, TaskDone); return err }, "legal moves: running"},
		{"task to bogus", func() error { _, err := TransitionTask(s, x.ID, "bogus"); return err }, "legal moves: running"},
		{"missing plan", func() error { _, err := TransitionPlan(s, "nope", PlanActive); return err }, `no plan "nope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("illegal move accepted, want rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}

	// Terminal states refuse everything.
	if _, err := TransitionTask(s, x.ID, TaskRunning); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if _, err := TransitionTask(s, x.ID, TaskDone); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if _, err := TransitionTask(s, x.ID, TaskRunning); err == nil {
		t.Fatal("done→running accepted, want rejection")
	} else if !strings.Contains(err.Error(), "legal moves: failed") {
		t.Fatalf("wrong moves error: %q", err.Error())
	}
}

func TestPlanCompletionGuards(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	x, _ := CreateTask(s, a.ID, "x", "do it")
	draftBlock(t, s, x.ID)
	if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if _, err := TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatalf("setup: %s", err)
	}

	// Approach cannot complete with an open task.
	if _, err := TransitionApproach(s, a.ID, ApproachComplete); err == nil {
		t.Fatal("complete with open task accepted, want rejection")
	} else if !strings.Contains(err.Error(), "1 open or unverified tasks") {
		t.Fatalf("wrong guard error: %q", err.Error())
	}
	// Plan cannot complete with an open approach.
	if _, err := TransitionPlan(s, p.ID, PlanComplete); err == nil {
		t.Fatal("complete with open approach accepted, want rejection")
	} else if !strings.Contains(err.Error(), "open or unverified approach") {
		t.Fatalf("wrong guard error: %q", err.Error())
	}
	// Close everything: both pass.
	if _, err := TransitionTask(s, x.ID, TaskRunning); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if _, err := TransitionTask(s, x.ID, TaskDone); err != nil {
		t.Fatalf("setup: %s", err)
	}
	recordCompleted(t, s, x.ID)
	if _, err := TransitionApproach(s, a.ID, ApproachComplete); err != nil {
		t.Fatalf("approach→complete: %s", err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanComplete); err != nil {
		t.Fatalf("plan→complete: %s", err)
	}
}

func TestPlanNoOrphans(t *testing.T) {
	s := open(t)
	if _, err := CreateApproach(s, "missing", "a"); err == nil {
		t.Fatal("approach without plan accepted, want rejection")
	}
	if _, err := CreateTask(s, "missing", "x", "do it"); err == nil {
		t.Fatal("task without approach accepted, want rejection")
	}
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err == nil {
		t.Fatal("ready with zero tasks accepted, want rejection")
	}
	if _, err := CreatePlan(s, "", "g"); err == nil {
		t.Fatal("empty title accepted, want rejection")
	}
	if _, err := CreateTask(s, a.ID, "x", ""); err == nil {
		t.Fatal("empty prompt accepted, want rejection")
	}
}

func TestPlanReadyHook(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	x, _ := CreateTask(s, a.ID, "x", "do it")
	draftBlock(t, s, x.ID)
	if _, err := Ready(context.Background(), s, a.ID, func(context.Context, *Plan, []*Task) error { return errTestHook }); err != errTestHook {
		t.Fatalf("hook error not propagated, got %v", err)
	}
}

// draftBlock stores a valid block on a task for gate-passing setups.
func draftBlock(t *testing.T, s *store.Store, id string) {
	t.Helper()
	_, err := SetTaskBlock(s, id, "now", "after", "mechanism", "exit code is 0", "TestX", "go test ./...", "true", "test 1 -eq 1")
	if err != nil {
		t.Fatalf("draft: %s", err)
	}
}

func TestPlanReadyGateValidation(t *testing.T) {
	setup := func(t *testing.T) (*store.Store, string) {
		t.Helper()
		s := open(t)
		p, _ := CreatePlan(s, "p", "g")
		a, _ := CreateApproach(s, p.ID, "a")
		return s, a.ID
	}

	// Undrafted block: blank field named.
	s, aid := setup(t)
	x, _ := CreateTask(s, aid, "x", "do it")
	if _, err := fixtureApproachTransition(s, aid, ApproachReady); err == nil {
		t.Fatal("ready with blank block accepted, want rejection")
	} else if !strings.Contains(err.Error(), `field "current" is blank`) {
		t.Fatalf("wrong error: %q", err.Error())
	}

	// Blank acceptance still refuses before readiness.
	if _, err := SetTaskBlock(s, x.ID, "now", "after", "mech", " \n", "TestX", "run it", "true", "test 1 -eq 1"); err != nil {
		t.Fatalf("set block: %s", err)
	}
	if _, err := fixtureApproachTransition(s, aid, ApproachReady); err == nil {
		t.Fatal("ready with blank acceptance accepted, want rejection")
	} else if !strings.Contains(err.Error(), `field "acceptance" is blank`) {
		t.Fatalf("wrong error: %q", err.Error())
	}

	// Nonblank behavioral prose passes the mechanical gate (fresh approach: no
	// ready→draft move exists, by design).
	for _, ok := range []string{"Current question matches the active question", "exit code is 1", "run `go test ./...`", `count(*) is 3`} {
		s, aid := setup(t)
		x, _ := CreateTask(s, aid, "x", "do it")
		if _, err := SetTaskBlock(s, x.ID, "now", "after", "mech", ok, "TestX", "run it", "true", "test 1 -eq 1"); err != nil {
			t.Fatalf("set block: %s", err)
		}
		if _, err := fixtureApproachTransition(s, aid, ApproachReady); err != nil {
			t.Fatalf("acceptance %q rejected: %s", ok, err)
		}
	}
}

func TestPlanBlockFieldsRequired(t *testing.T) {
	task := &Task{Current: "observed", Target: "correct", Sketch: "repair", Acceptance: "Current question matches the active question", Tests: "regression", Verify: "inspect independently", AcceptanceCommand: "test -d ."}
	if err := ValidateTaskBlock(task); err != nil {
		t.Fatalf("complete behavioral brief refused: %v", err)
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"current", &task.Current}, {"target", &task.Target}, {"sketch", &task.Sketch}, {"acceptance", &task.Acceptance}, {"tests", &task.Tests}, {"verify", &task.Verify}, {"acceptance_command", &task.AcceptanceCommand},
	} {
		original := *field.value
		*field.value = " \n"
		if err := ValidateTaskBlock(task); err == nil || !strings.Contains(err.Error(), `field "`+field.name+`" is blank`) {
			t.Errorf("blank %s admitted or unnamed: %v", field.name, err)
		}
		*field.value = original
	}
}

func TestPlanNextUnknown(t *testing.T) {
	if _, err := NextPlan("bogus"); err == nil {
		t.Fatal("unknown plan state accepted, want error")
	}
	if _, err := NextTask(""); err == nil {
		t.Fatal("empty task state accepted, want error")
	}
}

// completeTree builds a fully complete plan→approach→task chain for
// retirement tests. Every level walks its legal chain to complete.
func completeTree(t *testing.T, s *store.Store, title string) (pid, aid, xid string) {
	t.Helper()
	p, err := CreatePlan(s, title, "g")
	if err != nil {
		t.Fatalf("create plan: %s", err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatalf("plan→active: %s", err)
	}
	a, err := CreateApproach(s, p.ID, title)
	if err != nil {
		t.Fatalf("create approach: %s", err)
	}
	x, err := CreateTask(s, a.ID, title, "do it")
	if err != nil {
		t.Fatalf("create task: %s", err)
	}
	draftBlock(t, s, x.ID)
	if _, err := TransitionTask(s, x.ID, TaskRunning); err != nil {
		t.Fatalf("task→running: %s", err)
	}
	if _, err := TransitionTask(s, x.ID, TaskDone); err != nil {
		t.Fatalf("task→done: %s", err)
	}
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatalf("approach→ready: %s", err)
	}
	if _, err := TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatalf("approach→running: %s", err)
	}
	recordCompleted(t, s, x.ID)
	if _, err := TransitionApproach(s, a.ID, ApproachComplete); err != nil {
		t.Fatalf("approach→complete: %s", err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanComplete); err != nil {
		t.Fatalf("plan→complete: %s", err)
	}
	return p.ID, a.ID, x.ID
}

func TestPlanRetireChain(t *testing.T) {
	s := open(t)
	pid, aid, _ := completeTree(t, s, "p")
	a, err := TransitionApproach(s, aid, ApproachRetired)
	if err != nil {
		t.Fatalf("retire approach: %s", err)
	}
	if a.State != ApproachRetired {
		t.Fatalf("approach state=%s", a.State)
	}
	if m, _ := NextApproach(a.State); len(m) != 0 {
		t.Fatalf("retired approach next=%v, want none", m)
	}
	p, err := TransitionPlan(s, pid, PlanRetired)
	if err != nil {
		t.Fatalf("retire plan: %s", err)
	}
	if p.State != PlanRetired {
		t.Fatalf("plan state=%s", p.State)
	}
	if m, _ := NextPlan(p.State); len(m) != 0 {
		t.Fatalf("retired plan next=%v, want none", m)
	}
}

func TestPlanRetireIncomplete(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	x, _ := CreateTask(s, a.ID, "x", "do it")
	draftBlock(t, s, x.ID)
	reject := func(name string, call func() error, want string) {
		t.Helper()
		if err := call(); err == nil {
			t.Fatalf("%s accepted, want rejection", name)
		} else if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %q, want substring %q", name, err.Error(), want)
		}
	}
	reject("plan draft→retired", func() error {
		_, err := TransitionPlan(s, p.ID, PlanRetired)
		return err
	}, "legal moves: active")
	reject("approach draft→retired", func() error {
		_, err := TransitionApproach(s, a.ID, ApproachRetired)
		return err
	}, "legal moves: ready")
	if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatalf("plan→active: %s", err)
	}
	reject("plan active→retired", func() error {
		_, err := TransitionPlan(s, p.ID, PlanRetired)
		return err
	}, "legal moves: complete")
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatalf("approach→ready: %s", err)
	}
	reject("approach ready→retired", func() error {
		_, err := TransitionApproach(s, a.ID, ApproachRetired)
		return err
	}, "legal moves: running")
	if _, err := TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatalf("approach→running: %s", err)
	}
	reject("approach running→retired", func() error {
		_, err := TransitionApproach(s, a.ID, ApproachRetired)
		return err
	}, "legal moves: complete")
	if p, _ := GetPlan(s, p.ID); p.State != PlanActive {
		t.Fatalf("plan state=%s after rejections", p.State)
	}
	if a, _ := GetApproach(s, a.ID); a.State != ApproachRunning {
		t.Fatalf("approach state=%s after rejections", a.State)
	}
}

func TestPlanBuckets(t *testing.T) {
	s := open(t)
	pid, aid, _ := completeTree(t, s, "old")
	p2, _ := CreatePlan(s, "new", "g")
	a2, _ := CreateApproach(s, p2.ID, "b")
	if _, err := TransitionApproach(s, aid, ApproachRetired); err != nil {
		t.Fatalf("retire approach: %s", err)
	}
	if _, err := TransitionPlan(s, pid, PlanRetired); err != nil {
		t.Fatalf("retire plan: %s", err)
	}
	active, err := ActivePlans(s)
	if err != nil {
		t.Fatalf("active: %s", err)
	}
	if len(active) != 1 || active[0].ID != p2.ID {
		t.Fatalf("active plans=%v, want only new", ids(active))
	}
	retired, err := RetiredPlans(s)
	if err != nil {
		t.Fatalf("retired: %s", err)
	}
	if len(retired) != 1 || retired[0].ID != pid {
		t.Fatalf("retired plans=%v, want only old", ids(retired))
	}
	if aa, _ := ActivePlanApproaches(s, pid); len(aa) != 0 {
		t.Fatalf("old plan active approaches=%d, want 0", len(aa))
	}
	ra, _ := RetiredPlanApproaches(s, pid)
	if len(ra) != 1 || ra[0].ID != aid {
		t.Fatalf("old plan retired approaches wrong: %d", len(ra))
	}
	aa2, _ := ActivePlanApproaches(s, p2.ID)
	if len(aa2) != 1 || aa2[0].ID != a2.ID {
		t.Fatalf("new plan active approaches wrong: %d", len(aa2))
	}
}

func ids(ps []*Plan) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func recordCompleted(t *testing.T, s *store.Store, id string) {
	t.Helper()
	task, err := GetTask(s, id)
	if err != nil {
		t.Fatal(err)
	}
	task.Execution = outcome.Completed
	task.Verification = outcome.Passed
	task.AttemptLog = pairedFixtureLog(t, task)
	if err := SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
}

// Synthetic owner receipts exercise lifecycle predicates, not executed behavior.
func pairedFixtureLog(t *testing.T, task *Task) string {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{
		{"Kind": "check", "Command": task.CheckCommand, "Cwd": "/component", "Attempt": task.Attempts, "Started": "start", "Ended": "end", "episode_started": "start", "episode_ended": "end", "ExitCode": 0, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed", "acceptance": map[string]any{"command": task.AcceptanceCommand, "cwd": "/component", "started": "start", "ended": "end", "exit_code": 0, "status": "passed"}},
		{"Kind": "review", "Attempt": task.Attempts, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
