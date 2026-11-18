package agent_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

// unclosedThenCompleteModel first stops cleanly with an unclosed native JSON
// envelope (no length terminal, no declared prefix), then completes the same
// call on the next generation. The runner must retain the partial in context
// and continue the same task instead of terminating with a model error.
type unclosedThenCompleteModel struct {
	requests [][]llm.Message
}

func (m *unclosedThenCompleteModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, nil
}

func (m *unclosedThenCompleteModel) ToolProtocol() string { return "structured" }

func (m *unclosedThenCompleteModel) ChatTools(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.requests = append(m.requests, append([]llm.Message(nil), messages...))
	switch len(m.requests) {
	case 1:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: `<tool_call>{"name":"record","arguments":{"value":"ok"`, NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 100, Out: 9, Reported: true}}, nil
	case 2:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: `<tool_call>{"name":"record","arguments":{"value":"ok"}}</tool_call>`, NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 120, Out: 11, Reported: true}}, nil
	default:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: "done", NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 140, Out: 2, Reported: true}}, nil
	}
}

func TestUnclosedNativeGenerationContinuesSameTask(t *testing.T) {
	model := &unclosedThenCompleteModel{}
	sink := &nativeRecordingSink{}
	effects := 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{Text: "recorded"}, nil
	}})
	r := &agent.Runner{Model: model, Registry: reg, Session: sink, SessionID: "unclosed-continue", MaxSteps: 3, MaxTokens: 262144}
	_, _, err := r.Run(t.Context(), "record once")
	if err != nil || effects != 1 || r.Outcome.Status != outcome.Completed {
		t.Fatalf("unclosed generation did not continue to one completion: effects=%d outcome=%+v err=%v", effects, r.Outcome, err)
	}
	if len(model.requests) != 3 {
		t.Fatalf("requests = %d", len(model.requests))
	}
	retained := ""
	for _, message := range model.requests[1] {
		retained += message.Content + "\n"
	}
	if !strings.Contains(retained, "Unverified unfinished native response") || !strings.Contains(retained, `"value":"ok"`) {
		t.Fatalf("second generation did not carry the retained partial in context: %q", retained)
	}
}

// unclosedThenSuffixModel reproduces a transport refusal after an open frame,
// followed by a generation that literally continues from the last byte rather
// than restating the whole call. The runner must stitch both physical replies
// into one logical generation and execute the completed call exactly once.
type unclosedThenSuffixModel struct {
	requests [][]llm.Message
}

func (m *unclosedThenSuffixModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, nil
}

func (m *unclosedThenSuffixModel) ToolProtocol() string { return "structured" }

func (m *unclosedThenSuffixModel) ChatTools(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.requests = append(m.requests, append([]llm.Message(nil), messages...))
	switch len(m.requests) {
	case 1:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: `<tool_call>{"name":"record","arguments":{"value":"ok"`, NativeInitialState: "normal"}, fmt.Errorf("native completion refused")
	case 2:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: `}}</tool_call>`, NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 120, Out: 4, Reported: true}}, nil
	default:
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: "done", NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 140, Out: 2, Reported: true}}, nil
	}
}

func TestUnclosedNativeSuffixContinuesSameLogicalGeneration(t *testing.T) {
	model := &unclosedThenSuffixModel{}
	sink := &nativeRecordingSink{}
	effects := 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{Text: "recorded"}, nil
	}})
	r := &agent.Runner{Model: model, Registry: reg, Session: sink, SessionID: "unclosed-suffix", MaxSteps: 3, MaxTokens: 262144}
	_, _, err := r.Run(t.Context(), "record once")
	if err != nil || effects != 1 || r.Outcome.Status != outcome.Completed {
		t.Fatalf("suffix continuation did not complete exactly once: effects=%d outcome=%+v err=%v", effects, r.Outcome, err)
	}
	if len(model.requests) != 3 {
		t.Fatalf("requests = %d", len(model.requests))
	}
}
