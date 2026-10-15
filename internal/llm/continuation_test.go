package llm

import "testing"

func TestNativeMessagesPreserveAllOriginalThinkingFields(t *testing.T) {
	m := Message{Role: "assistant", Content: "original text", Reasoning: "thought A", ReasoningContent: "thought B", Thinking: "thought C"}
	wire := nativeMessages([]Message{m})[0]
	if wire["reasoning"] != m.Reasoning || wire["reasoning_content"] != m.ReasoningContent || wire["thinking"] != m.Thinking {
		t.Fatalf("native serializer lost original auxiliary evidence: %+v", wire)
	}
}
