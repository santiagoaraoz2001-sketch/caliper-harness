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

// Old consumed reasoning and tool-call argument bodies prune like old tool
// outputs (approved contract change: they otherwise accumulate inside
// protected groups until compaction cannot reduce retained context).
// Visible text, call correlation and recent bodies stay exact.
func TestPrunePreservesOldReasoningWhilePruningToolOutput(t *testing.T) {
	r := &Runner{Prune: Prune{OutputsAgeTokens: 100}}
	old := session.Entry{Kind: "reply", Message: llm.Message{
		Role: "assistant", Content: "kept text", Reasoning: strings.Repeat("think ", 400),
		ToolCalls: []llm.ToolCall{{ID: "a", Name: "read", Arguments: `{"path":"` + strings.Repeat("p", 1000) + `"}`}},
	}}
	filler := session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "a", Content: strings.Repeat("x", 5000)}}
	recent := session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Reasoning: strings.Repeat("fresh ", 400)}}
	c := &session.Continuation{Entries: []session.Entry{old, filler, recent}}
	_, note := r.prune(c)
	if note == "" {
		t.Fatal("expected a prune report")
	}
	if !strings.HasPrefix(c.Entries[0].Message.Reasoning, "[pruned reasoning") {
		t.Fatal("old reasoning did not prune past the age bound")
	}
	if got := c.Entries[0].Message.ToolCalls; len(got) != 1 || got[0].ID != "a" || got[0].Name != "read" || !strings.Contains(got[0].Arguments, `"_pruned"`) {
		t.Fatalf("old call correlation lost or arguments not stubbed: %+v", got)
	}
	if !strings.HasPrefix(c.Entries[1].Message.Content, "[pruned tool output") || c.Entries[1].Message.ToolCallID != "a" {
		t.Fatal("old tool output did not prune with its call correlation intact")
	}
	if c.Entries[0].Message.Content != "kept text" {
		t.Fatal("visible text was modified")
	}
	if c.Entries[2].Message.Reasoning != recent.Message.Reasoning {
		t.Fatal("recent reasoning must stay verbatim")
	}
}
