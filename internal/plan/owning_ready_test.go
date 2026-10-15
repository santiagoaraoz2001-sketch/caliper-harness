package plan

import (
	"context"
	"errors"
	"specific-harness/internal/store"
	"strings"
	"sync"
	"testing"
)

func TestOwningReadyUncheckedRefuses(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	task, _ := CreateTask(s, a.ID, "t", "p")
	draftBlock(t, s, task.ID)
	if _, err := TransitionApproach(s, a.ID, ApproachReady); err == nil || !strings.Contains(err.Error(), "Ready") {
		t.Errorf("unchecked ready admitted: %v", err)
	}
	got, _ := GetApproach(s, a.ID)
	if got.State != ApproachDraft {
		t.Error("unchecked ready mutated", got.State)
	}
}

func TestOwningReadySnapshotAndRace(t *testing.T) {
	makeDraft := func() (*store.Store, string, string) {
		s := open(t)
		p, _ := CreatePlan(s, "goal", "complete goal")
		a, _ := CreateApproach(s, p.ID, "approach")
		x, _ := CreateTask(s, a.ID, "task", "prompt")
		draftBlock(t, s, x.ID)
		return s, a.ID, x.ID
	}
	for _, mode := range []string{"absent", "refused", "cancelled", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, id, tid := makeDraft()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var check ReadyChecker = func(ctx context.Context, p *Plan, tasks []*Task) error {
				if len(tasks) != 1 || tasks[0].Current == "" || tasks[0].Verify == "" || p.Goal != "complete goal" {
					t.Fatal("incomplete snapshot")
				}
				switch mode {
				case "refused":
					return errors.New("real refusal")
				case "cancelled":
					cancel()
				case "changed":
					_, err := SetTaskBlock(s, tid, "changed", "target", "sketch", "exit code 0", "tests", "verify")
					return err
				}
				return nil
			}
			if mode == "absent" {
				check = nil
			}
			if _, err := Ready(ctx, s, id, check); err == nil {
				t.Fatal("unsafe readiness admitted")
			}
			a, _ := GetApproach(s, id)
			x, _ := GetTask(s, tid)
			if a.State != ApproachDraft || x.Attempts != 0 {
				t.Fatalf("mutated: %+v %+v", a, x)
			}
		})
	}
	s, id, _ := makeDraft()
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Ready(context.Background(), s, id, func(context.Context, *Plan, []*Task) error { arrived <- struct{}{}; <-release; return nil })
			results <- err
		}()
	}
	<-arrived
	<-arrived
	close(release)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("atomic wins=%d", wins)
	}
}
