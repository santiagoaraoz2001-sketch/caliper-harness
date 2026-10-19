package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func nativeFramingFixture(draft, boundary string, index int, terminal string) string {
	wire := map[string]any{"error": map[string]any{
		"message": "unfinished native tool call", "native_tool_draft": draft,
		"native_tool_failure": ToolFramingError{Draft: draft, Reason: "unfinished native tool call", Boundary: boundary, CallIndex: index},
	}}
	if terminal != "" {
		wire["done"], wire["done_reason"] = true, terminal
	}
	b, _ := json.Marshal(wire)
	return string(b) + "\n"
}

func TestNativeToolFramingPreservesEvidence(t *testing.T) {
	// The captured Qwen failure repeats parameters and ends without closing
	// its tool. Transport preserves those bytes; it does not pick an argument.
	draft := "<tool_call><function=plan>\n" + strings.Repeat("<parameter=articles>gameplay\ninterface\nintegration</parameter>\n", 31)
	prefix := `{"model":"served","message":{"thinking":"Let me draft the plan.","tool_calls":[{"function":{"name":"read","arguments":{"path":"a.go"}}}]},"prompt_eval_count":12033,"eval_count":4789}` + "\n"
	for _, terminal := range []string{"", "stop"} {
		t.Run("terminal="+terminal, func(t *testing.T) {
			c, _ := stubClient(t, 200, prefix+nativeFramingFixture(draft, "stop", 1, terminal))
			c.WithRuntime(RuntimeOllama)
			c.thinkingMeta = &thinkingMetadata{} // Exercise chat transfer, not metadata probing.
			var displayed strings.Builder
			reply, err := c.ChatTools(t.Context(), nil, []ToolDefinition{fixtureTool()}, func(s string) { displayed.WriteString(s) })
			var framing *ToolFramingError
			if !errors.As(err, &framing) || framing.Draft != draft || framing.Boundary != "stop" || framing.CallIndex != 1 {
				t.Fatalf("framing evidence lost: %#v %v", framing, err)
			}
			if reply.Message.Thinking != "Let me draft the plan." || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].ID == "" || reply.Message.ToolCalls[0].Name != "read" {
				t.Fatalf("prior unexecuted evidence lost: %#v", reply)
			}
			if reply.Usage != (Usage{12033, 4789, true}) || reply.Served != "served" || reply.OutputLimit || reply.ThinkingOnly || displayed.Len() != 0 {
				t.Fatalf("failure became answer/continuation or lost metadata: %#v %q", reply, displayed.String())
			}
		})
	}
}

func TestNativeToolFramingRejectsUnsettledTransfers(t *testing.T) {
	frame := nativeFramingFixture("<tool_call><function=plan>", "stop", 0, "")
	for _, tc := range []struct {
		name, body           string
		broken, cancel, text bool
	}{
		{name: "length", body: nativeFramingFixture("plan", "stop", 0, "length")},
		{name: "after-length", body: `{"done":true,"done_reason":"length"}` + "\n" + frame},
		{name: "after-stop", body: `{"done":true,"done_reason":"stop"}` + "\n" + frame},
		{name: "unknown-boundary", body: nativeFramingFixture("plan", "length", 0, "")},
		{name: "bad-index", body: nativeFramingFixture("plan", "stop", 2, "")},
		{name: "data-after", body: frame + `{"message":{"content":"tail"}}` + "\n"},
		{name: "done-after", body: frame + `{"done":true,"done_reason":"stop"}` + "\n"},
		{name: "bad-JSON-after", body: frame + "{bad\n"},
		{name: "broken-transfer", body: frame, broken: true},
		{name: "cancelled", body: frame, cancel: true},
		{name: "text-only", body: frame, text: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stubClient(t, 200, tc.body)
			c.WithRuntime(RuntimeOllama)
			c.thinkingMeta = &thinkingMetadata{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.broken {
				c.http = &http.Client{Transport: bodyErrorTransport{body: &failingBody{prefix: strings.NewReader(tc.body)}}}
			}
			if tc.cancel {
				c.http = &http.Client{Transport: bodyErrorTransport{body: &cancelAtEOF{Reader: strings.NewReader(tc.body), cancel: cancel}}}
			}
			tools := []ToolDefinition{fixtureTool()}
			if tc.text {
				tools = nil
			}
			_, err := c.ChatTools(ctx, nil, tools, nil)
			var framing *ToolFramingError
			if err == nil || errors.As(err, &framing) {
				t.Fatalf("unsettled or non-tool failure admitted: %v", err)
			}
			if tc.broken && !errors.Is(err, io.ErrUnexpectedEOF) || tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("transport/cancellation cause lost: %v", err)
			}
		})
	}
}

type cancelAtEOF struct {
	*strings.Reader
	cancel context.CancelFunc
}

func (b *cancelAtEOF) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b *cancelAtEOF) Close() error { return nil }
