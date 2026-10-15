package launch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestPATCreationSelectsOnlyItsActualNewNode(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := Deps{Store: s, Workspace: t.TempDir()}
	defs := NewBuild(d)
	call := func(name string, args tool.Args) tool.Result {
		for _, def := range defs {
			if def.Name == name {
				r, e := def.Execute(context.Background(), args)
				if e != nil {
					t.Fatal(e)
				}
				if r.Created == nil || r.Focus == nil || r.Focus.Store != s.Identity() || r.Focus.ID != r.Created.ID || r.Focus.Kind != r.Created.Kind {
					t.Fatalf("creation did not select its owning new node: %+v", r)
				}
				return r
			}
		}
		t.Fatal("missing", name)
		return tool.Result{}
	}
	p := call("workflow_plan", tool.Args{"title": "current", "goal": "new outcome"})
	a := call("workflow_approach", tool.Args{"plan": p.Created.ID, "title": "strategy"})
	x := call("workflow_task", tool.Args{"approach": a.Created.ID, "title": "first", "prompt": "first work"})
	y := call("workflow_task", tool.Args{"approach": a.Created.ID, "title": "second", "prompt": "second work"})
	if x.Focus.ID == y.Focus.ID {
		t.Fatal("sibling creation selected stale task")
	}
	tasks, e := plan.ApproachTasks(s, a.Created.ID)
	if e != nil || len(tasks) != 2 {
		t.Fatal(tasks, e)
	}
}

func TestPATRealWorkerTaskInstructionsAreProtectedSystem(t *testing.T) {
	s, aid, _ := setup(t, 1)
	workers, reviewers := 0, 0
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		for _, m := range messages {
			if m.Role == "user" && strings.HasPrefix(m.Content, "Review ") {
				reviewers++
				return "PASS: mechanical component check", nil
			}
		}
		workers++
		task, behavior := 0, 0
		for _, m := range messages {
			if m.Role == "system" && m.Content == "PAT_BEHAVIOR" {
				behavior++
			}
			if strings.Contains(m.Content, "Task t0: prompt-t0") {
				if m.Role != "system" {
					t.Error("current worker task was not at protected system role", m.Role)
				} else if !strings.Contains(m.Content, prompts.Builtin().Text("system.task.handoff")) {
					t.Error("worker lost shared handoff paragraph")
				}
				task++
			}
		}
		if task != 1 || behavior != 1 {
			t.Errorf("task/behavior duplicated or omitted: %d/%d", task, behavior)
		}
		return "worker component execution complete", nil
	})
	d := depsFor(s, model)
	d.Workspace = bindFixtureProject(t, s, aid, t.TempDir())
	d.Behavior = "PAT_BEHAVIOR"
	d.Wf.VerifyFollowup = true
	results, e := Launch(t.Context(), d, aid)
	if e != nil || len(results) != 1 || !results[0].Qualified() || results[0].Outcome.Status != outcome.Completed || workers != 1 || reviewers != 1 {
		t.Fatalf("system role broke actual receipts: %+v %v worker/review%d/%d", results, e, workers, reviewers)
	}
}
