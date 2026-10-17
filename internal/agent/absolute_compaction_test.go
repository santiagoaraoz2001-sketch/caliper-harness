package agent

import (
	"context"
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

// Regression for the 2026-10-17 live stop: at 93,141 tokens on a 262,144
// window the absolute ceiling fired but Keep=40% (104,858 tokens) retained
// everything, so compaction found no summary material and the length path
// stopped the task. The keep budget must shrink under the ceiling so
// compaction always has material whenever the ceiling fires.
func TestCompactRunsWhenAbsoluteFiresBelowKeepPercent(t *testing.T) {
	c := Compaction{Trigger: 90, Keep: 40, Summary: 10, Step: 1, Max: 20, WordsPerToken: 0.5, Absolute: 65536}
	if got := keepBudgetTokens(c, 262144, 0); got != 65536-26214 {
		t.Fatalf("keep budget: got %v, want %v", got, 65536-26214)
	}
	if got := keepBudgetTokens(testCompaction(), 262144, 0); got != 40.0/100*262144 {
		t.Fatalf("percent-only keep budget changed: %v", got)
	}
	model := &scriptModel{replies: []string{"factual summary"}}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 262144,
		TemplatesDir: testTemplateDir(t), Compaction: c}
	msgs := []llm.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, llm.Message{Role: "user", Content: strings.Repeat("old ", 2000)})
		msgs = append(msgs, llm.Message{Role: "assistant", Content: strings.Repeat("done ", 2000)})
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: "fresh"})
	_, _, ran, err := testCompact(&r, context.Background(), msgs, 93141, 0)
	if err != nil || !ran {
		t.Fatalf("compaction at 93k with 65k ceiling: ran=%t err=%v", ran, err)
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
