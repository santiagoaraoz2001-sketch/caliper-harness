package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// The percent trigger alone fires at 235k on a 262k window; the absolute
// ceiling must fire at measured occupancy long before the model degrades.
func TestShouldCompactAbsoluteCeiling(t *testing.T) {
	c := Compaction{Trigger: 90, Absolute: 65536}
	if !shouldCompact(70000, 262144, true, c) {
		t.Fatal("70k measured tokens must fire the 65k absolute ceiling")
	}
	if shouldCompact(60000, 262144, true, c) {
		t.Fatal("60k must waitbelow the absolute ceiling")
	}
	if shouldCompact(70000, 262144, true, Compaction{Trigger: 90}) {
		t.Fatal("without Absolute, 70k stays under the 90% trigger")
	}
	if !shouldCompact(240000, 262144, true, Compaction{Trigger: 90}) {
		t.Fatal("percent trigger still fires without Absolute")
	}
	if shouldCompact(70000, 262144, true, Compaction{Trigger: 0, Absolute: 65536}) {
		t.Fatal("Trigger 0 disables automatic summaries entirely")
	}
}

// Settled reasoning older than the prune horizon is stubbed; recent
// reasoning, calls and visible text stay exact.
func TestPruneStubsOldReasoningOnly(t *testing.T) {
	r := &Runner{Prune: Prune{OutputsAgeTokens: 100}}
	old := session.Entry{Kind: "reply", Message: llm.Message{
		Role: "assistant", Content: "kept text", Reasoning: strings.Repeat("think ", 400),
		ToolCalls: []llm.ToolCall{{ID: "a", Name: "read", Arguments: "{}"}},
	}}
	filler := session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "a", Content: strings.Repeat("x", 5000)}}
	recent := session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Reasoning: strings.Repeat("fresh ", 400)}}
	c := &session.Continuation{Entries: []session.Entry{old, filler, recent}}
	_, note := r.prune(c)
	if note == "" {
		t.Fatal("expected a prune report")
	}
	if !strings.HasPrefix(c.Entries[0].Message.Reasoning, "[pruned reasoning") {
		t.Fatalf("old reasoning not stubbed: %q", c.Entries[0].Message.Reasoning[:40])
	}
	if c.Entries[0].Message.Content != "kept text" || len(c.Entries[0].Message.ToolCalls) != 1 {
		t.Fatal("visible text or calls were modified")
	}
	if !strings.HasPrefix(c.Entries[2].Message.Reasoning, "fresh ") {
		t.Fatal("recent reasoning must stay verbatim")
	}
}
