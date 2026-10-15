package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixtureTool() ToolDefinition {
	var d ToolDefinition
	d.Type = "function"
	d.Function.Name = "bash"
	d.Function.Parameters = map[string]any{"type": "object"}
	return d
}
func TestStructuredFragmentsAndDirectTextRefusal(t *testing.T) {
	args := "{\"n\":9007199254740993,\"cmd\":\"  a\\r\\nb\\t  \"}"
	requests := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] == true {
			for i, part := range []string{args[:12], args[12:]} {
				d := map[string]any{"index": 0, "function": map[string]string{"arguments": part}}
				if i == 0 {
					d["id"] = "stable"
					d["type"] = "function"
					d["function"].(map[string]string)["name"] = "bash"
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{d}}}}})
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "{\"choices\":[{\"message\":{\"tool_calls\":[{\"id\":\"stable\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}")
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "fixture")
	r, e := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, nil)
	if e != nil || len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].Arguments != args {
		t.Fatalf("reply=%+v err=%v", r, e)
	}
	if _, _, _, e := c.Chat(context.Background(), nil); e == nil {
		t.Fatal("direct text ignored native calls")
	}
	c.WithToolProtocol("text")
	if _, e := c.ChatTools(context.Background(), []Message{r.Message}, nil, nil); e == nil || !strings.Contains(e.Error(), "new chat") {
		t.Fatalf("native history route switch not refused: %v", e)
	}
	if len(requests) != 2 {
		t.Fatalf("refused route switch forwarded request: %d", len(requests))
	}
}
func TestStructuredNativeObjectsAndCapabilityRefusal(t *testing.T) {
	for _, support := range []bool{true, false} {
		t.Run(fmt.Sprint(support), func(t *testing.T) {
			calls := 0
			rawArgs := "{ \"cmd\" : \"x\\r\\n  y\", \"n\":9007199254740993 }"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/show" {
					if support {
						fmt.Fprint(w, "{\"capabilities\":[\"tools\"]}")
					} else {
						fmt.Fprint(w, "{\"capabilities\":[\"completion\"]}")
					}
					return
				}
				if r.URL.Path != "/api/chat" {
					t.Error("wrong origin/path")
				}
				calls++
				var req map[string]any
				json.NewDecoder(r.Body).Decode(&req)
				if req["stream"] != true || req["tools"] == nil {
					t.Error("native request lacks stream/tools")
				}
				fmt.Fprintf(w, "{\"model\":\"fixture\",\"message\":{\"tool_calls\":[{\"function\":{\"name\":\"bash\",\"arguments\":%s}}]},\"done\":true,\"done_reason\":\"stop\"}\n", rawArgs)
			}))
			defer srv.Close()
			c := New(srv.URL, "fixture").WithRuntime("ollama")
			reply, e := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, nil)
			if !support {
				if e == nil || calls != 0 {
					t.Fatalf("unsupported forwarded %d %v", calls, e)
				}
				return
			}
			if e != nil || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].ID == "" || reply.Message.ToolCalls[0].Arguments != rawArgs {
				t.Fatalf("native=%+v err=%v", reply, e)
			}
		})
	}
}
func TestToolInactivityHeadersProgressAndCallerCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(35 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 0; i < 3; i++ {
			time.Sleep(35 * time.Millisecond)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New(srv.URL, "fixture").WithToolProtocol("text")
	c.idleTimeout = 60 * time.Millisecond
	c.http.Timeout = 10 * time.Millisecond
	start := time.Now()
	r, e := c.ChatTools(context.Background(), nil, nil, nil)
	if e != nil || r.Message.Content != "xxx" || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("progress=%+v err=%v", r, e)
	}
	c.idleTimeout = 10 * time.Millisecond
	if _, e = c.ChatTools(context.Background(), nil, nil, nil); e == nil || !strings.Contains(e.Error(), "inactive") {
		t.Fatalf("idle not refused %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.ChatTools(ctx, nil, nil, nil); e == nil || !strings.Contains(e.Error(), "canceled") {
		t.Fatalf("cancellation hidden %v", e)
	}
}
func TestStructuredTerminalFailuresRetainCalls(t *testing.T) {
	call := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"id\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]}}]}\n\n"
	for _, tail := range []string{"", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n", "data: [DONE]\n\ndata: {\"error\":\"late\"}\n\n", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n"} {
		t.Run(tail, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, call+tail) }))
			defer srv.Close()
			r, e := New(srv.URL, "fixture").ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, nil)
			if e == nil || len(r.Message.ToolCalls) != 1 {
				t.Fatalf("partial admitted/lost %+v %v", r, e)
			}
		})
	}
}
