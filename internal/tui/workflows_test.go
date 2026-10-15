package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// wfModel builds a model on a temp store: plan P with a running
// approach (one done task, attempts recorded), a ready approach
// (one queued task), and a complete approach (one done task).
func wfModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(title string, complete bool, attempts int) string {
		p, err := plan.CreatePlan(s, "P", "g")
		if err != nil {
			t.Fatal(err)
		}
		a, err := plan.CreateApproach(s, p.ID, title)
		if err != nil {
			t.Fatal(err)
		}
		x, err := plan.CreateTask(s, a.ID, title+"-t", "do it")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.SetTaskBlock(s, x.ID, "n", "t", "s", "exit code 0", "T", "run it", "printf UI_FIXTURE_SUITE", "printf UI_FIXTURE_ACCEPTANCE"); err != nil {
			t.Fatal(err)
		}
		if _, err := fixtureApproachTransition(s, a.ID, plan.ApproachReady); err != nil {
			t.Fatal(err)
		}
		if title == "B-ready" {
			return a.ID
		}
		if _, err := plan.TransitionApproach(s, a.ID, plan.ApproachRunning); err != nil {
			t.Fatal(err)
		}
		if _, err := plan.TransitionTask(s, x.ID, plan.TaskRunning); err != nil {
			t.Fatal(err)
		}
		if err := plan.SetTaskAttempts(s, x.ID, attempts); err != nil {
			t.Fatal(err)
		}
		if err := plan.SetTaskResult(s, x.ID, "out"); err != nil {
			t.Fatal(err)
		}
		if _, err := plan.TransitionTask(s, x.ID, plan.TaskDone); err != nil {
			t.Fatal(err)
		}
		recordTUICompleted(t, s, x.ID)
		if complete {
			if _, err := plan.TransitionApproach(s, a.ID, plan.ApproachComplete); err != nil {
				t.Fatal(err)
			}
		}
		return a.ID
	}
	mk("A-run", false, 2)
	mk("B-ready", false, 0)
	mk("C-done", true, 1)
	s.Close()
	m := New(testCfg(), "test")
	m.StorePath = filepath.Join(dir, "db")
	return m
}

func loadWf(m Model) Model {
	um, _ := m.Update(m.refreshWorkflows()())
	return um.(Model)
}

func TestWorkflowsOrderAndShape(t *testing.T) {
	m := loadWf(wfModel(t))
	if m.Wf.Err != "" {
		t.Fatalf("err=%s", m.Wf.Err)
	}
	if len(m.Wf.Rows) != 2 {
		t.Fatalf("complete hides by default: %+v", m.Wf.Rows)
	}
	if m.Wf.Rows[0].Title != "A-run" || m.Wf.Rows[1].Title != "B-ready" {
		t.Fatalf("running first: %+v", m.Wf.Rows)
	}
	r := m.Wf.Rows[0]
	if r.State != plan.ApproachRunning || r.Done != 1 || r.Total != 1 || r.Attempts != 2 {
		t.Errorf("row=%+v", r)
	}
	if r.Step != "settled" {
		t.Errorf("step=%q", r.Step)
	}
	out := m.renderWorkflows(100, 30)
	for _, want := range []string{"A-run", "running", "1/1", "2 attempts", "l launch", "p pause/resume", "complete:hidden"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestWorkflowsToggleComplete(t *testing.T) {
	m := submit(wfModel(t), "/workflows")
	m = loadWf(m)
	m = key(m, "c")
	m = loadWf(m)
	if len(m.Wf.Rows) != 3 || m.Wf.Rows[2].Title != "C-done" {
		t.Fatalf("complete last: %+v", m.Wf.Rows)
	}
	m = key(m, "c")
	m = loadWf(m)
	if len(m.Wf.Rows) != 2 {
		t.Fatalf("toggle back: %+v", m.Wf.Rows)
	}
}

func TestWorkflowsViewOpensAndEscapes(t *testing.T) {
	m := wfModel(t)
	m = submit(m, "/workflows")
	if !m.workflowsActive() {
		t.Fatal("not on the workflows view")
	}
	m = loadWf(m)
	if len(m.Wf.Rows) != 2 {
		t.Fatalf("rows=%+v", m.Wf.Rows)
	}
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Fatal("esc must walk workflows back to chat")
	}
}

func TestWorkflowsBusyRefuses(t *testing.T) {
	m := loadWf(wfModel(t))
	m.Wf.Busy = "some-id"
	n := len(m.Alerts)
	if cmd := m.wfLaunch(m.Wf.Rows[0], false); cmd != nil {
		t.Fatal("second launch must refuse")
	}
	if len(m.Alerts) != n+1 {
		t.Fatal("refusal must say so")
	}
}

func TestWorkflowsPauseKey(t *testing.T) {
	m := submit(wfModel(t), "/workflows")
	m = loadWf(m)
	m = key(m, "p")
	m = loadWf(m)
	if m.Wf.Rows[0].State != plan.ApproachPaused {
		t.Fatalf("rows=%+v", m.Wf.Rows)
	}
	// p on a ready approach explains instead of acting.
	m.Wf.Sel = 1
	n := len(m.Alerts)
	m = key(m, "p")
	if len(m.Alerts) != n+1 || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "pause needs running") {
		t.Fatalf("alerts=%+v", m.Alerts)
	}
}

func TestWorkflowsDive(t *testing.T) {
	m := submit(wfModel(t), "/workflows")
	m = loadWf(m)
	m.Wf.Sel = 1
	m = key(m, "enter")
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Fatal("enter must land on the plans tree")
	}
	// The tree arrives async; the pending dive opens detail on load.
	m = loadWf(m)
	um, _ := m.Update(m.refreshPlans("")())
	m = um.(Model)
	if m.Plans.Detail == nil {
		t.Fatal("dive must open the approach detail")
	}
}

func TestWorkflowsEmpty(t *testing.T) {
	m := New(testCfg(), "test")
	m.StorePath = filepath.Join(t.TempDir(), "db")
	m = loadWf(m)
	if m.Wf.Err != "" {
		t.Fatalf("fresh db must open clean: %s", m.Wf.Err)
	}
	if len(m.Wf.Rows) != 0 {
		t.Fatalf("rows=%+v", m.Wf.Rows)
	}
	if out := m.renderWorkflows(80, 20); !strings.Contains(out, "no approaches yet") {
		t.Errorf("empty state must say so:\n%s", out)
	}
}

func TestWorkflowsLiveAgentCount(t *testing.T) {
	a := &plan.Approach{ID: "a", Title: "A", State: plan.ApproachRunning}
	tasks := []*plan.Task{
		{ID: "t1", Title: "hot", State: plan.TaskRunning},
		{ID: "t2", Title: "next", State: plan.TaskQueued},
		{ID: "t3", Title: "old", State: plan.TaskDone, Execution: outcome.Completed, Verification: outcome.NotRequired},
	}
	pairedTUIFixture(t, tasks[2])
	r := wfRow("P", a, tasks)
	if r.Agents != 1 || r.Step != "hot" || r.Done != 1 {
		t.Fatalf("row=%+v", r)
	}
	m := New(testCfg(), "test")
	m.Wf.Rows = []WfRow{r}
	if out := m.renderWorkflows(100, 20); !strings.Contains(out, "1 agents") {
		t.Errorf("row must show live agents:\n%s", out)
	}
}

func recordTUICompleted(t *testing.T, s *store.Store, id string) {
	t.Helper()
	task, err := plan.GetTask(s, id)
	if err != nil {
		t.Fatal(err)
	}
	pairedTUIFixture(t, task)
	if err := s.Write(`MATCH(x:Task) WHERE x.id=$id SET x.check_command=$check,x.acceptance_command=$acceptance,x.attempts=$attempt`, map[string]any{"id": task.ID, "check": task.CheckCommand, "acceptance": task.AcceptanceCommand, "attempt": task.Attempts}); err != nil {
		t.Fatal(err)
	}
	if err := plan.SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
}

// Synthetic paired receipts exercise UI counts/lifecycle, not behavioral coverage.
func pairedTUIFixture(t *testing.T, task *plan.Task) {
	t.Helper()
	task.Execution, task.Verification = outcome.Completed, outcome.Passed
	task.CheckCommand, task.AcceptanceCommand = "printf UI_FIXTURE_SUITE", "printf UI_FIXTURE_ACCEPTANCE"
	if task.Attempts == 0 {
		task.Attempts = 1
	}
	raw, err := json.Marshal([]map[string]any{
		{"Kind": "check", "Command": task.CheckCommand, "Cwd": "/component", "Attempt": task.Attempts, "Started": "start", "Ended": "end", "episode_started": "start", "episode_ended": "end", "ExitCode": 0, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed, "acceptance": map[string]any{"command": task.AcceptanceCommand, "cwd": "/component", "started": "start", "ended": "end", "exit_code": 0, "status": "passed"}},
		{"Kind": "review", "Attempt": task.Attempts, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed, "Output": "PASS synthetic UI fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	task.AttemptLog = string(raw)
}
