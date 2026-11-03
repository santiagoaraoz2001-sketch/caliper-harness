package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/session"
)

// A settled check receipt must surface in completion evidence even when this
// runner has no coding obligation of its own (the reviewer case: the receipt
// exists and passed, yet evidence used to report it as unverified).
func TestCompletionEvidenceIncludesSettledReceiptWithoutObligation(t *testing.T) {
	zero := 0
	r := &Runner{}
	r.Continuation.Coding = &session.CodingState{
		Source:  "assigned",
		Command: "npm run check",
		Receipt: &session.CheckReceipt{
			Command: "npm run check", Cwd: "C:/work", Status: "passed", ExitCode: &zero,
		},
	}
	if r.CodingObligation() {
		t.Fatal("fixture must not carry a coding obligation")
	}
	text := r.completionEvidence()
	if !strings.Contains(text, "Declared check") || !strings.Contains(text, `status="passed"`) {
		t.Fatalf("settled receipt missing from evidence: %q", text)
	}

	bare := &Runner{}
	if got := bare.completionEvidence(); got != "" {
		t.Fatalf("no obligation and no receipt must stay silent, got %q", got)
	}
}
