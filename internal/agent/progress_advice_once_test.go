package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

func adviceFixture(t *testing.T, judge func(decision.Request) (decision.Verdict, error)) (*Runner, *watcher) {
	t.Helper()
	facts := BookkeepingFacts{Checkpoint: ProgressCheckpoint{Phase: "EXECUTE", PATStep: "claimed", SelectedID: "current"}}
	r := &Runner{Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "advice-once", LoopStall: 100,
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "current"}},
		Assigned:     &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.Continuation.Add("intent", "user", "retain this exact user request", false)
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) { return judge(q) }
	return r, &watcher{}
}

// A loop-confirm judgment already reviewed this settled phase; the discovery
// review that follows in the same settle must not judge the same failure run
// a second time.
func TestProgressAdviceSettlesFailureRunOnce(t *testing.T) {
	attempts := 0
	r, w := adviceFixture(t, func(q decision.Request) (decision.Verdict, error) {
		attempts++
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	})
	log := func(string, string) error { return nil }
	for i := 0; i < 3; i++ {
		w.discoveryReceipt(&observed{Tool: "bash", Result: toolResultFailed()}, 100)
	}
	packet, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.progressAdvice(t.Context(), w, packet, "loop-confirm", "instructions", log); err != nil || attempts != 1 {
		t.Fatalf("loop-confirm did not judge once: attempts=%d err=%v", attempts, err)
	}
	if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 1 {
		t.Fatalf("discovery re-judged the same failure run: attempts=%d err=%v", attempts, err)
	}
}

// When protected evidence structurally cannot fit the judge, retrying the
// same advisory packet class every settle only repeats a doomed admission.
// Nothing is truncated to force a fit; the unavailability is proven once by
// an actual refusal, then advice stops being attempted for this runner.
func TestProgressAdviceStopsAfterCapacityExhaustion(t *testing.T) {
	attempts := 0
	r, w := adviceFixture(t, func(q decision.Request) (decision.Verdict, error) {
		attempts++
		return decision.Verdict{}, &decision.ContextCapacityError{Message: "provider context limit"}
	})
	errKinds := []string{}
	log := func(kind, _ string) error { errKinds = append(errKinds, kind); return nil }
	for i := 0; i < 2; i++ {
		packet, err := r.progressEvidence(t.Context(), w)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.progressAdvice(t.Context(), w, packet, "discovery-checkpoint", "instructions", log); err != nil {
			t.Fatalf("advice failure must stay advisory: %v", err)
		}
	}
	if attempts != 1 {
		t.Fatalf("doomed advisory packet retried: attempts=%d, want 1", attempts)
	}
	judgmentErrors := 0
	for _, k := range errKinds {
		if k == "judgment-error" {
			judgmentErrors++
		}
	}
	if judgmentErrors != 1 {
		t.Fatalf("unavailability must be disclosed exactly once, got %d (%s)", judgmentErrors, strings.Join(errKinds, ","))
	}
}

func TestProgressAdviceReprobesAfterCompactionOrRevision(t *testing.T) {
	attempts := 0
	r, w := adviceFixture(t, func(q decision.Request) (decision.Verdict, error) {
		attempts++
		return decision.Verdict{}, &decision.ContextCapacityError{Message: "provider context limit"}
	})
	log := func(string, string) error { return nil }
	probe := func(want int) {
		t.Helper()
		packet, err := r.progressEvidence(t.Context(), w)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.progressAdvice(t.Context(), w, packet, "discovery-checkpoint", "instructions", log); err != nil {
			t.Fatal(err)
		}
		if attempts != want {
			t.Fatalf("attempts=%d, want %d", attempts, want)
		}
	}
	probe(1)
	probe(1) // Same compaction and workflow revision: still exhausted.
	r.Continuation.Compactions++
	probe(2) // A published compaction can change the protected packet.
	probe(2)
	r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "task", ID: "revised"}
	probe(3) // A revised workflow selection is a new advisory basis.
	probe(3)
}
