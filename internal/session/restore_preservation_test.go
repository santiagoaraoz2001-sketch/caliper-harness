package session

import (
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func TestRestorePressureRetainsReasoningSchemasAndCompleteNativeGroups(t *testing.T) {
	c := Continuation{Version: 1, Entries: []Entry{
		{Kind: "intent", Message: llm.Message{Role: "user", Content: "original requirement"}},
		{Kind: "reply", Message: llm.Message{Role: "assistant", Reasoning: strings.Repeat("actual old reasoning ", 30), ToolCalls: []llm.ToolCall{{ID: "stable", Name: "read", Arguments: `{"path":"owner.go"}`}}}},
		{Kind: "output", Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "stable", Content: strings.Repeat("actual output\n", 30)}},
		{Kind: "schema", Message: llm.Message{Role: "user", Content: "current tool schema"}},
		{Kind: "reply", Message: llm.Message{Role: "assistant", Thinking: "actual original thought", Content: "actual response"}},
	}}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, notice, err := Restore([]Event{{Kind: "continuation", Text: raw}}, 80)
	if err != nil || !reflect.DeepEqual(restored.Entries, c.Entries) || !strings.Contains(notice, "retained history") {
		t.Fatalf("pressure silently removed original context: %v notice=%q entries=%+v", err, notice, restored.Entries)
	}
}
