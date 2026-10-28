package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestR3DraftFeedbackLoadedBindingPersistsWithoutChat(t *testing.T) {
	for _, bound := range []bool{true, false} {
		name := "legacy_unbound"
		if bound {
			name = "bound"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			root, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			m := New(testCfg(), "component")
			m.Width, m.Height = 120, 40
			m.StorePath = filepath.Join(dir, "db")
			s, err := store.Open(m.StorePath)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if bound {
				ctx = plan.WithProject(ctx, root)
			}
			p, err := plan.CreatePlanContext(ctx, s, "draft", "review only")
			if err != nil {
				t.Fatal(err)
			}
			a, err := plan.CreateApproach(s, p.ID, "strategy")
			if err != nil {
				t.Fatal(err)
			}
			task, err := plan.CreateTask(s, a.ID, "task", "no implementation")
			if err != nil {
				t.Fatal(err)
			}
			before, err := plan.ReadinessSnapshot(s, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()

			loaded, err := loadPlans(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
			if err != nil {
				t.Fatal(err)
			}
			m.Plans = PlansState{Plans: loaded, Loaded: true}
			m.Plans.rebuild(task.ID)
			m.navigate(ViewPlans, "")
			m = key(m, "f")
			if m.Input.Value() != "/comment P1-A1-T1 " {
				t.Fatalf("actual shortcut: %q", m.Input.Value())
			}
			text := "Keep café β🙂 readable; plan only"
			m = submit(m, m.Input.Value()+text)
			if m.Chat.Running || len(m.Chat.SessionIDs) != 0 {
				t.Fatal("feedback started a model turn")
			}

			// Reopen actual Kuzu after the UI command; neither injected LoadedPlan
			// metadata nor the in-memory next-message queue proves persistence.
			s, err = store.Open(m.StorePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := plan.GetTask(s, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			comments, err := outcome.TaskComments(got.AttemptLog, 0)
			if err != nil {
				t.Fatal(err)
			}
			if bound {
				if len(comments) != 1 || comments[0].Source != "user" || comments[0].Attempt != 0 {
					t.Fatalf("draft feedback not owned: %+v; alert=%+v", comments, lastAlert(m))
				}
				var payload Comment
				if err := json.Unmarshal([]byte(comments[0].Text), &payload); err != nil || payload.Text != text || payload.Ref != "P1-A1-T1" {
					t.Fatalf("payload=%+v err=%v", payload, err)
				}
				if !strings.Contains(lastAlert(m).Text, "draft task feedback saved") {
					t.Fatal("durable placement not reported")
				}
				if err := plan.RecordCommentDelivery(ctx, s, comments[0], "worker"); err == nil {
					t.Fatal("draft feedback granted worker authority")
				}
			} else if len(comments) != 0 || len(m.Comment) != 1 {
				t.Fatal("unbound plan silently gained durable task feedback authority")
			}
			after, err := plan.ReadinessSnapshot(s, a.ID)
			if err != nil || before != after || got.State != plan.TaskQueued || got.Attempts != 0 || !got.SameBrief(task) {
				t.Fatal("feedback changed task/readiness authority", err)
			}
		})
	}
}
