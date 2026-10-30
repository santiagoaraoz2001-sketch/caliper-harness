package run

import (
	"context"
	"encoding/json"

	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestPreparationReadinessBudgetBeforeReady(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Decision.CoherenceMaxChars = 3000 // legacy accepted value; no byte ceiling enforces it
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Coding.CheckCommand = "true"
	r, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Model: "component", Window: 32768, MaxSteps: 5, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "budget"}, "repair")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Registry.Lookup("prepare_task")
	input := tool.Args{"plan_title": "repair", "plan_goal": "requested behavior", "approach_title": "bounded repair", "task_title": "repair", "task_prompt": "repair behavior", "current": strings.Repeat("é\"\\\n", 900), "target": "expected behavior", "sketch": "inspect owner and add regression", "acceptance": "correct behavior", "tests": "regression", "verify": "independent source and assertion review", "acceptance_command": "test -d .", "check_command": "true"}
	raw, _ := json.Marshal(input)
	args, why := tool.ValidateJSON(d, string(raw))
	if why != "" {
		t.Fatal(why)
	}
	bound, args, err := d.Bind(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Exec(context.Background(), bound, args)
	if err != nil || result.Failed {
		t.Fatalf("Draft must survive overage: %v %s", err, result.Text)
	}
	r.Assigned.Observe(bound, args, result, err)
	var receipt graph.Result
	if err = json.Unmarshal([]byte(result.Text), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Effects) != 4 {
		t.Fatal(result.Text)
	}
	aid := receipt.Effects[1].ID
	tid := receipt.Effects[3].ID
	task, err := plan.GetTask(s, tid)
	if err != nil || task.State != plan.TaskQueued || task.Current != input["current"] {
		t.Fatal("stored brief altered", task, err)
	}
	facts, err := r.Assigned.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preparation := facts.(agent.BookkeepingFacts).Preparation
	if !strings.Contains(preparation, "Readiness packet:") || !strings.Contains(preparation, "ready_task(task=") {
		t.Fatalf("missing current actionable budget: %s", preparation)
	}
	ready, _ := r.Registry.Lookup("ready_task")
	ready, args, err = ready.Bind(context.Background(), tool.Args{"task": tid})
	if err != nil {
		t.Fatal(err)
	}
	refused, err := r.Exec(context.Background(), ready, args)
	// No byte ceiling: Ready reaches the decision service (unavailable in
	// this fixture) instead of refusing on size before judgment.
	if err == nil || strings.Contains(err.Error(), "no judgment ran") || strings.Contains(err.Error(), "coherence_max_chars") {
		t.Fatalf("actual Ready must not retain byte cap: %v %s", err, refused.Text)
	}
	a, err := plan.GetApproach(s, aid)
	if err != nil || a.State != plan.ApproachDraft {
		t.Fatal("diagnostic/readiness failure transitioned approach", a, err)
	}
}

func TestDefaultReadySendsCompleteBriefToDecisionAdmission(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
	current := strings.Repeat("observed source κ\n", 350)
	judgments := 0
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		judgments++
		if !strings.Contains(q.Context, current) || !strings.Contains(q.Context, "acceptance_command: test -d .") || !strings.Contains(q.Context, "check_command: true") {
			t.Error("complete authored brief/commands did not reach the decision admission")
		}
		return 0
	})
	p, err := plan.CreatePlan(s, "actual request", "requested behavior")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "bounded behavior repair")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "repair", "repair the observed behavior")
	if err != nil {
		t.Fatal(err)
	}
	check, acceptance := "true", "test -d ."
	if _, err = plan.SetTaskBlockCommandsContext(t.Context(), s, task.ID, current, "expected behavior", "repair owner and regression", "behavior becomes correct", "planned regression", "independent source review", &check, &acceptance); err != nil {
		t.Fatal(err)
	}
	r, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Model: "component", Window: 32768, MaxSteps: 5,
		Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "native-readiness"}, "repair")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.BindProject(t.Context(), s, p.ID, r.Workspace); err != nil {
		t.Fatal(err)
	}
	ready, _ := r.Registry.Lookup("ready_task")
	bound, args, err := ready.Bind(t.Context(), tool.Args{"task": task.ID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Exec(t.Context(), bound, args)
	if err != nil || result.Failed || judgments != 1 {
		t.Fatalf("default did not reach existing decision admission: %s %v judgments=%d", result.Text, err, judgments)
	}
	got, err := plan.GetApproach(s, a.ID)
	if err != nil || got.State != plan.ApproachReady {
		t.Fatalf("owning readiness missing: %+v %v", got, err)
	}
	if gotTask, err := plan.GetTask(s, task.ID); err != nil || gotTask.State != plan.TaskQueued || gotTask.Attempts != 0 {
		t.Fatalf("readiness fabricated claim: %+v %v", gotTask, err)
	}
}
