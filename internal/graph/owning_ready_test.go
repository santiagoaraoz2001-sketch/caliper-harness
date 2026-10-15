package graph

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"testing"
)

func TestOwningGraphReadiness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p, _ := plan.CreatePlan(s, "plan", "goal")
	a, _ := plan.CreateApproach(s, p.ID, "approach")
	x, _ := plan.CreateTask(s, a.ID, "task", "prompt")
	plan.SetTaskBlock(s, x.ID, "current", "target", "sketch", "exit code 0", "tests", "LATE_VERIFY", "true", "test 1 -eq 1")
	query := fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id state}}`, a.ID)
	if _, err := New(s, "test", t.TempDir()).Exec(context.Background(), query, "", nil); err == nil {
		t.Fatal("unchecked Graph Ready passed")
	}
	got, _ := plan.GetApproach(s, a.ID)
	if got.State != plan.ApproachDraft {
		t.Fatal(got.State)
	}
	calls := 0
	allow := false
	api := NewWithReady(s, "test", t.TempDir(), func(ctx context.Context, id string) (*plan.Approach, error) {
		return plan.Ready(ctx, s, id, func(_ context.Context, p *plan.Plan, tasks []*plan.Task) error {
			calls++
			if len(tasks) != 1 || tasks[0].Verify != "LATE_VERIFY" || tasks[0].CheckCommand != "true" || p.Goal != "goal" {
				t.Fatal("incomplete Graph checker")
			}
			if !allow {
				return errors.New("refused")
			}
			return nil
		})
	})
	if _, err := api.Exec(context.Background(), query, "", nil); err == nil {
		t.Fatal("refusal passed")
	}
	allow = true
	if _, err := api.Exec(context.Background(), query, "", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = plan.GetApproach(s, a.ID)
	task, _ := plan.GetTask(s, x.ID)
	if got.State != plan.ApproachReady || task.Attempts != 0 || task.Verify != "LATE_VERIFY" {
		t.Fatalf("readback %+v %+v", got, task)
	}
}
