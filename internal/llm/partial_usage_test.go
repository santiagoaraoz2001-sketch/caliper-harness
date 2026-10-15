package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The native MLX length frame precedes its usage trailer. Accounting must
// retain that trailer without turning partial output into an executable reply.
func TestPartialFinishRetainsTrailingUsage(t *testing.T) {
	const partial = "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"before\"},\"finish_reason\":\"length\"}]}\n\n"
	const usage = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":32}}\n\n"
	for _, tc := range []struct{ name, tail, cause string }{
		{"done", "data: [DONE]\n\n", ""},
		{"eof", "", ""},
		{"later-stop", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", ""},
		{"bad-json", "data: {broken\n\n", "bad stream chunk"},
		{"server-error", "data: {\"error\":\"later engine failure\"}\n\n", "later engine failure"},
		{"late-content", "data: {\"choices\":[{\"delta\":{\"content\":\"forbidden\"}}]}\n\n", "after stop"},
		{"late-call", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"forbidden\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]}}]}\n\n", "after stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("wrong fixture origin: %s", r.URL)
					http.Error(w, "wrong origin", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, partial+usage+tc.tail)
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "fixture").WithRuntime(RuntimeMLX)
			var deltas strings.Builder
			reply, err := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, func(s string) { deltas.WriteString(s) })
			if err == nil || !strings.Contains(err.Error(), "length") || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("lost failed finish/later cause: %v", err)
			}
			if reply.Usage != (Usage{7, 32, true}) || reply.FinishReason != "length" || reply.Served != "fixture" || reply.Message.Content != "before" || deltas.String() != "before" || len(reply.Message.ToolCalls) != 0 {
				t.Fatalf("lost accounting or admitted late output: %+v, deltas %q", reply, deltas.String())
			}
		})
	}
}
