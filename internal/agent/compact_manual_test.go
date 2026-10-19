package agent

import (
	"context"
	"errors"
	"reflect"
	"specific-harness/internal/decision"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestManualCompactionApprovalPreservesSourceAndNoJudge(t *testing.T) {
	for _, accept := range []bool{false, true} {
		r := Runner{ManualDecisions: true, Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
			t.Fatal("disabled judge called")
			return decision.Verdict{}, nil
		}, Ask: func(_ context.Context, p string) bool {
			if !strings.Contains(p, "precise summary") || !strings.Contains(p, "complete source") {
				t.Fatalf("missing review evidence: %s", p)
			}
			return accept
		}}
		got, err := r.reviewCompaction(context.Background(), session.Continuation{}, "complete source", "precise summary")
		if err != nil || got != accept {
			t.Fatalf("approval %v %v", got, err)
		}
	}
	r := Runner{ManualDecisions: true}
	if ok, err := r.reviewCompaction(context.Background(), session.Continuation{}, "source", "summary"); ok || err == nil {
		t.Fatal("headless summary admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.Ask = func(context.Context, string) bool { cancel(); return true }
	if ok, err := r.reviewCompaction(ctx, session.Continuation{}, "source", "summary"); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled summary admitted: %v %v", ok, err)
	}
}

func TestManualCompactionActualReplacementRequiresApproval(t *testing.T) {
	for _, accept := range []bool{false, true} {
		before := truthFixture()
		model := &scriptModel{replies: []string{"Repair pending; actual commands failed."}}
		r := truthRunner(t, model, 10000)
		r.ManualDecisions = true
		r.Judge = nil
		r.Ask = func(context.Context, string) bool { return accept }
		after, _, ran, err := r.Compact(context.Background(), before)
		if err != nil || ran != accept {
			t.Fatalf("actual compaction ran%v err%v", ran, err)
		}
		if !accept && !reflect.DeepEqual(before, after) {
			t.Fatal("declined summary changed context")
		}
		if accept && after.Compactions != before.Compactions+1 {
			t.Fatal("accepted summary not committed")
		}
	}
}
