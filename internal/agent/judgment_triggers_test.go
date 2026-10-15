package agent

import (
	"context"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Three consecutive failed calls are a stuck signal: advisory review must
// fire on that trigger even when neither cadence nor a phase change applies.
func TestDiscoveryReviewFiresAfterConsecutiveFailures(t *testing.T) {
	facts := BookkeepingFacts{Checkpoint: ProgressCheckpoint{Phase: "EXECUTE", PATStep: "claimed", SelectedID: "current"}}
	r := &Runner{Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "failure-trigger", LoopStall: 100,
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "current"}},
		Assigned:     &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.Continuation.Add("intent", "user", "retain this exact user request", false)
	attempts := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		attempts++
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}
	w := &watcher{}
	log := func(string, string) error { return nil }
	if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 0 {
		t.Fatalf("initial phase judged without a trigger: attempts=%d err=%v", attempts, err)
	}
	for i := 0; i < 3; i++ {
		w.discoveryReceipt(&observed{Tool: "bash", Result: toolResultFailed()}, 100)
	}
	if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 1 {
		t.Fatalf("consecutive failures did not trigger one review: attempts=%d err=%v", attempts, err)
	}
	if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 1 {
		t.Fatalf("failure trigger re-fired without new failures: attempts=%d err=%v", attempts, err)
	}
}

func toolResultFailed() tool.Result { return tool.Result{Failed: true, Text: "command failed"} }
