package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Large windows use the same 90-percent rule as smaller windows.
func TestCompactionOnlyAtWindowPercentage(t *testing.T) {
	for _, window := range []int{131072, 262144} {
		for _, tokens := range []int{65536, 70000, int(float64(window) * .9), int(float64(window)*.9) + 1} {
			want := float64(tokens) >= .9*float64(window)
			if got := shouldCompact(tokens, window, true, Compaction{Trigger: 90}); got != want {
				t.Fatalf("window=%d tokens=%d got=%v want=%v", window, tokens, got, want)
			}
		}
	}
}

// Only old tool-output bodies are pruned; reasoning, calls and visible text stay exact.
func TestPrunePreservesOldReasoningWhilePruningToolOutput(t *testing.T) {
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
	if c.Entries[0].Message.Reasoning != old.Message.Reasoning {
		t.Fatal("old reasoning changed")
	}
	if !strings.HasPrefix(c.Entries[1].Message.Content, "[pruned tool output") || c.Entries[1].Message.ToolCallID != "a" {
		t.Fatal("old tool output did not prune with its call correlation intact")
	}
	if c.Entries[0].Message.Content != "kept text" || len(c.Entries[0].Message.ToolCalls) != 1 {
		t.Fatal("visible text or calls were modified")
	}
	if c.Entries[2].Message.Reasoning != recent.Message.Reasoning {
		t.Fatal("recent reasoning must stay verbatim")
	}
}
