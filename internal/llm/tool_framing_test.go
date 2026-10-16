package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func framingFixture(draft, boundary string, index int) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "duplicate or empty native GLM argument key", "native_tool_draft": draft, "native_tool_failure": ToolFramingError{Draft: draft, Reason: "duplicate or empty native GLM argument key", Boundary: boundary, CallIndex: index}}})
	return "data: " + string(b) + "\n\n"
}

func TestToolFramingFailurePreservesCapturedEvidence(t *testing.T) {
	for _, draft := range []string{"plan><arg_key></arg_key><arg_value>{}</arg_value>", "plan<arg_key>argument_keys></arg_key><arg_value>{}</arg_value><arg_key>argument_values>"} {
		t.Run(draft, func(t *testing.T) {
			prefix := `data: {"choices":[{"delta":{"reasoning":"worker intent", "tool_calls":[{"index":0,"id":"prior-call","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}}]}` + "\n\n"
			c, _ := stubClient(t, 200, prefix+framingFixture(draft, "tool_close", 1))
			reply, err := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, nil)
			var framing *ToolFramingError
			if !errors.As(err, &framing) || framing.Draft != draft || framing.Boundary != "tool_close" || framing.CallIndex != 1 {
				t.Fatalf("lost framing: %#v %v", framing, err)
			}
			if reply.Message.Reasoning != "worker intent" || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].ID != "prior-call" {
				t.Fatalf("lost partial evidence: %#v", reply)
			}
			if reply.OutputLimit || reply.ThinkingOnly {
				t.Fatal("failure became continuation/success")
			}
		})
	}
}

func TestToolFramingFailureRejectsUnsettledTransfers(t *testing.T) {
	frame := framingFixture("plan><arg_key></arg_key><arg_value>{}</arg_value>", "stop", 0)
	for _, tc := range []struct {
		name, body     string
		broken, cancel bool
	}{
		{"length", `data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" + frame, false, false},
		{"unknown-boundary", framingFixture("plan>", "length", 0), false, false},
		{"bad-index", framingFixture("plan>", "stop", 2), false, false},
		{"data-after", frame + `data: {"choices":[{"delta":{"content":"tail"}}]}` + "\n\n", false, false},
		{"broken-transfer", frame, true, false},
		{"cancelled", frame, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stubClient(t, 200, tc.body)
			if tc.broken {
				c.http = &http.Client{Transport: bodyErrorTransport{body: &failingBody{prefix: strings.NewReader(tc.body)}}}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			_, err := c.ChatTools(ctx, nil, []ToolDefinition{fixtureTool()}, nil)
			var framing *ToolFramingError
			if err == nil || errors.As(err, &framing) {
				t.Fatalf("unsettled failure admitted: %v", err)
			}
		})
	}
}
