package agent

import (
	"context"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestNativeHistoryRetainsGroupWithoutReplay(t *testing.T) {
	history := []llm.Message{{Role: "user", Content: "original"}, {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "old", Name: "missing", Arguments: `{}`}}}, {Role: "tool", ToolCallID: "old", Name: "missing", Content: "historical failure"}}
	model := &scriptModel{replies: []string{"answer"}}
	r := Runner{Model: model, Registry: tool.NewRegistry(), History: history, MaxSteps: 1}
	if _, _, e := r.Run(context.Background(), "new turn"); e != nil {
		t.Fatal(e)
	}
	if len(model.seen[0][2].ToolCalls) != 1 || model.seen[0][3].ToolCallID != "old" {
		t.Fatalf("History lost native metadata %+v", model.seen)
	}
	if e := r.Continuation.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestNativePruneAndCompactGroups(t *testing.T) {
	c := session.Continuation{Version: 1, Entries: []session.Entry{{Message: llm.Message{Role: "user", Content: "intent"}, Kind: "intent"}, {Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "one", Name: "bash", Arguments: `{"cmd":"echo original"}`}, {ID: "two", Name: "bash", Arguments: `{"cmd":"echo second"}`}, {ID: "three", Name: "bash", Arguments: `{"cmd":"echo third"}`}}}, Kind: "reply"}, {Message: llm.Message{Role: "tool", Name: "bash", ToolCallID: "one", Content: strings.Repeat("output ", 500)}, Kind: "output"}, {Message: llm.Message{Role: "tool", Name: "bash", ToolCallID: "two", Content: "second result"}, Kind: "output"}, {Message: llm.Message{Role: "tool", Name: "bash", ToolCallID: "three", Content: "third result"}, Kind: "output"}, {Message: llm.Message{Role: "user", Content: "current"}, Kind: "task"}}}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: &scriptModel{replies: []string{"short"}}, MaxTokens: 20000, TemplatesDir: "../../templates", Prune: Prune{OutputsPct: 1}, Compaction: Compaction{Keep: 0.1, Summary: 10, Max: 10, WordsPerToken: 1}}
	r.prune(&c)
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if c.Entries[2].Message.ToolCallID != "one" || c.Entries[1].Message.ToolCalls[0].Arguments != `{"cmd":"echo original"}` {
		t.Fatal("prune broke identity")
	}
	out, _, ran, e := r.Compact(context.Background(), c)
	if e != nil || !ran {
		t.Fatalf("compact %v %v", ran, e)
	}
	if e := out.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, entry := range out.Entries {
		if len(entry.Message.ToolCalls) > 0 || entry.Message.Role == "tool" {
			t.Fatal("half/old group survived")
		}
	}
}
