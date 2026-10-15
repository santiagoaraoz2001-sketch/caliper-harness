package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func TestCompactionTransportStreamsWithoutTools(t *testing.T) {
	for _, ending := range []string{"stop", "length", "call", "missing", "thinking"} {
		t.Run(ending, func(t *testing.T) {
			streamed, definitions := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]any
				json.NewDecoder(req.Body).Decode(&body)
				streamed, _ = body["stream"].(bool)
				_, definitions = body["tools"]
				w.Header().Set("Content-Type", "text/event-stream")
				if ending == "call" {
					fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"unrequested","type":"function","function":{"name":"bash","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":17,"completion_tokens":3}}`+"\n\ndata: [DONE]\n\n")
					return
				}
				delta := `"content":"Pending work; read owned source next."`
				if ending == "thinking" {
					delta = `"reasoning":"Need to inspect."`
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{%s}}]}\n\n", delta)
				reason := ending
				if ending == "thinking" {
					reason = "stop"
				}
				if ending != "missing" {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", reason)
				}
			}))
			defer server.Close()
			original := truthFixture()
			runner := truthRunner(t, &scriptModel{}, 32768)
			runner.Model = llm.New(server.URL, "fixture")
			next, summary, ran, err := runner.Compact(context.Background(), original)
			if !streamed || definitions {
				t.Errorf("summary wire stream=%t definitions=%t", streamed, definitions)
			}
			if ending == "stop" {
				if err != nil || !ran || !strings.Contains(summary, "Pending work") {
					t.Fatalf("progress refused: %v", err)
				}
			} else {
				if err == nil || ran || !reflect.DeepEqual(next, original) {
					t.Fatalf("incomplete/unrequested summary applied: %v", err)
				}
			}
			if ending != "missing" && (runner.UsedIn != 17 || runner.UsedOut != 3) {
				t.Errorf("usage lost: %d/%d", runner.UsedIn, runner.UsedOut)
			}
		})
	}
}
