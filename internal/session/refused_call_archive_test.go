package session

import (
	"testing"

	"specific-harness/internal/llm"
)

// Captured from the 2026-10-15 Laguna rerun attempts: the model wrapped
// plan arguments in invented nested objects ({"input":{}}, {"arg_key>":{}}).
// Execution validation refuses those gracefully, but archiving the refusal
// must also succeed: the saved call bytes are evidence, and archive
// integrity needs correlated groups over syntactically valid JSON objects,
// not execution-grade scalar arguments.
func TestRefusedNestedArgumentCallStillArchives(t *testing.T) {
	for _, args := range []string{`{"arg_key>": {}}`, `{"input": {"nested": [1, 2]}}`} {
		c := Continuation{Version: 1, Entries: []Entry{
			{Kind: "intent", Message: llm.Message{Role: "user", Content: "repair it"}},
			{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "plan", Arguments: args}}}},
			{Kind: "failure", Message: llm.Message{Role: "tool", Name: "plan", ToolCallID: "c1", Content: "entire call array refused"}},
		}}
		if err := c.Validate(); err != nil {
			t.Fatalf("refused call %s could not be archived: %v", args, err)
		}
	}
	c := Continuation{Version: 1, Entries: []Entry{
		{Kind: "intent", Message: llm.Message{Role: "user", Content: "repair it"}},
		{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "plan", Arguments: `{"a": `}}}},
		{Kind: "failure", Message: llm.Message{Role: "tool", Name: "plan", ToolCallID: "c1", Content: "refused"}},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("syntactically broken arguments archived without error")
	}
}
