package agent

import (
	"encoding/json"
	"testing"

	"specific-harness/internal/session"
)

// A focus change keeps the same conversation and model, so the latest
// server-measured prompt remains the occupancy basis: it must survive the
// publish and the emitted context reading must report it, never prompt=0.
// (c52 trace: every claim/navigation publish emitted prompt=0 measured=false
// while the live prompt was 150k-194k.)
func TestSettleFocusPreservesMeasuredPrompt(t *testing.T) {
	r := &Runner{Session: &recordSink{}, SessionID: "focus-prompt"}
	r.Continuation.Version = 1
	r.Continuation.BasisChars, r.Continuation.BasisTokens = 800000, 200000
	r.Continuation.Prompt, r.Continuation.Measured = 194422, true
	r.Continuation.ModelKey = "fixture-model"
	r.Continuation.Add("intent", "user", "original request", false)
	focus := session.WorkflowScope{Store: "owner", Kind: "task", ID: "t1"}
	r.pendingFocus = &focus
	var contextRaw string
	logFn := func(kind, text string) error {
		if kind == "context" {
			contextRaw = text
		}
		return nil
	}
	if err := r.settleFocus(logFn); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.Prompt != 194422 || !r.Continuation.Measured {
		t.Fatalf("focus change discarded measured prompt basis: prompt=%d measured=%t", r.Continuation.Prompt, r.Continuation.Measured)
	}
	if contextRaw == "" {
		t.Fatal("focus publish emitted no context reading")
	}
	var reading session.Continuation
	if err := json.Unmarshal([]byte(contextRaw), &reading); err != nil {
		t.Fatal(err)
	}
	if reading.Prompt != 194422 || !reading.Measured || reading.ModelKey != "fixture-model" {
		t.Fatalf("context reading misreports occupancy: prompt=%d measured=%t key=%q", reading.Prompt, reading.Measured, reading.ModelKey)
	}
}
