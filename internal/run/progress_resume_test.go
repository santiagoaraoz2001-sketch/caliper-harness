package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestPersistedAssignmentCheckpointRemainsPrepareAfterRunReset(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("source", []byte("observed source"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	task := assignedFixtureTask(t, s, "true")
	seen := false
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		var input struct {
			Checkpoint struct{ Phase, PATStep, SelectedID string }
		}
		if json.Unmarshal([]byte(q.Context), &input) == nil && input.Checkpoint.SelectedID == task.ID {
			seen = true
			if input.Checkpoint.Phase != "PREPARE" || input.Checkpoint.PATStep != "inspect-assignment" {
				t.Errorf("historical assignment became a current claim: %+v", input.Checkpoint)
			}
		}
		return 0
	})
	model := codingFixture(t, "structured", []codingCall{{"read", map[string]any{"path": "source"}}})
	cfg.Model.Server = model.URL
	scope, err := launch.SelectScope(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: scope}, Cfg: cfg, Store: s, Model: "fixture", Server: model.URL, MaxSteps: 2, Window: 32768,
		Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "resume-progress"}, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	r.Continuation.Version = 1 // initialize the real durable claim snapshot before this explicit tool operation
	r.Continuation.Add("intent", "user", "inspect requested behavior", false)
	start, _ := r.Registry.Lookup("task_start")
	if start.Bind != nil {
		var args tool.Args
		start, args, err = start.Bind(t.Context(), tool.Args{"task": task.ID})
		if err == nil {
			_, err = r.Exec(t.Context(), start, args)
		}
	} else {
		_, err = r.Exec(t.Context(), start, tool.Args{"task": task.ID})
	}
	if err != nil || r.Continuation.Assignment == nil {
		t.Fatalf("real initial claim missing: %v", err)
	}
	before, err := plan.GetTask(s, task.ID)
	if err != nil || before.Attempts != 1 || before.State != plan.TaskRunning {
		t.Fatalf("initial owning claim: %+v %v", before, err)
	}
	if _, _, err = r.Run(context.Background(), "resume inspection"); err != nil {
		t.Fatal(err)
	}
	after, err := plan.GetTask(s, task.ID)
	if err != nil || !seen || after.Attempts != 1 || after.State != plan.TaskRunning || !after.SameBrief(before) || r.ClaimedTaskScope(&tool.WorkflowScope{Store: s.Identity(), Kind: "task", ID: task.ID}) {
		t.Fatalf("resume fabricated authority/progress: %+v seen=%t %v", after, seen, err)
	}
}
