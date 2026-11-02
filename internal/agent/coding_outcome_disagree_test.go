package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// When the frozen acceptance fails in the same episode where the
// configured suite passes on the same workspace revision, the two
// commands disagree about the same code: the outcome reason must name
// the acceptance command itself as suspect instead of reporting a
// plain product failure.
func TestCodingOutcomeNamesDisagreeingAcceptance(t *testing.T) {
	exit := 1
	r := &Runner{Prompts: prompts.Builtin(), Continuation: session.Continuation{Coding: &session.CodingState{
		TaskID: "task", Attempt: 1,
		Receipt: &session.CheckReceipt{
			Status:     "passed",
			Acceptance: &session.CommandReceipt{Status: "failed", Reason: "command exited1", ExitCode: &exit},
		},
	}}}
	status, reason := r.codingOutcome()
	if status != outcome.Failed {
		t.Fatalf("status=%v want failed", status)
	}
	if !strings.Contains(reason, "configured suite passed") || !strings.Contains(reason, "acceptance command") {
		t.Fatalf("reason does not name the disagreement: %q", reason)
	}
	// When the suite also fails, keep the plain product-failure reason.
	r.Continuation.Coding.Receipt.Status = "failed"
	r.Continuation.Coding.Receipt.Reason = "command exited1"
	_, reason = r.codingOutcome()
	if strings.Contains(reason, "configured suite passed") {
		t.Fatalf("disagreement named when both failed: %q", reason)
	}
}
