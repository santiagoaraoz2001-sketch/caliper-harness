package agent

import (
	"testing"

	"specific-harness/internal/session"
)

// Changing workflow focus to another PAT node keeps the same model and
// conversation, so the measured chars-per-token calibration remains valid.
// Losing it makes pruning count bytes as tokens and overstate output age.
func TestSettleFocusPreservesTokenCalibration(t *testing.T) {
	r := &Runner{Session: &recordSink{}, SessionID: "focus-basis"}
	r.Continuation.Version = 1
	r.Continuation.BasisChars, r.Continuation.BasisTokens = 4000, 1000
	r.Continuation.ModelKey = "fixture-model"
	r.Continuation.Add("intent", "user", "original request", false)
	focus := session.WorkflowScope{Store: "owner", Kind: "task", ID: "t1"}
	r.pendingFocus = &focus
	if err := r.settleFocus(func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.BasisChars != 4000 || r.Continuation.BasisTokens != 1000 {
		t.Fatalf("focus change discarded calibration: chars=%d tokens=%d", r.Continuation.BasisChars, r.Continuation.BasisTokens)
	}
}
