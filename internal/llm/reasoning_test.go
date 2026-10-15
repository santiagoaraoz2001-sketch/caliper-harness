package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReturnedReasoningCallAndNextWire(t *testing.T) {
	const thought = " \t```tool\r\n\\t café  "
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		t.Run(runtime, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if runtime == RuntimeOllama && r.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["tools"]}`)
					return
				}
				path := "/chat/completions"
				if runtime == RuntimeOllama {
					path = "/api/chat"
				}
				if r.URL.Path != path {
					t.Errorf("wrong fixture origin %s", r.URL)
					http.Error(w, "wrong origin", 400)
					return
				}
				var req struct {
					Messages []map[string]any `json:"messages"`
				}
				if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
					t.Error(e)
				}
				requests++
				if requests == 2 {
					m := req.Messages[0]
					if runtime == RuntimeOllama {
						if m["thinking"] != thought || m["reasoning"] != nil || m["reasoning_content"] != nil {
							t.Errorf("native metadata %+v", m)
						}
					} else if m["reasoning"] != thought || m["reasoning_content"] != "other\r\n" || m["thinking"] != nil {
						t.Errorf("aliases changed %+v", m)
					}
					if req.Messages[1]["tool_call_id"] != "call" || m["tool_calls"] == nil {
						t.Error("lost call association")
					}
					if runtime == RuntimeOllama {
						fmt.Fprintln(w, `{"message":{"content":"done"},"done":true,"done_reason":"stop"}`)
					} else {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					}
					return
				}
				for _, part := range []string{thought[:3], thought[3:]} {
					if runtime == RuntimeOllama {
						json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"thinking": part}})
					} else {
						b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": part}}}})
						fmt.Fprintf(w, "data: %s\n\n", b)
					}
				}
				if runtime == RuntimeOllama {
					fmt.Fprintln(w, `{"message":{"tool_calls":[{"id":"call","function":{"name":"bash","arguments":{}}}]},"done":true,"done_reason":"stop"}`)
				} else {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"other\\r\\n\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			c := New(srv.URL, "fixture").WithRuntime(runtime)
			var visible strings.Builder
			reply, e := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, func(s string) { visible.WriteString(s) })
			if e != nil || len(reply.Message.ToolCalls) != 1 || reply.Message.Content != "" || visible.Len() != 0 {
				t.Fatalf("metadata became answer/call %+v %v", reply, e)
			}
			m := reply.Message
			if runtime == RuntimeOllama {
				if m.Thinking != thought {
					t.Fatal("native thinking changed")
				}
			} else if m.Reasoning != thought || m.ReasoningContent != "other\r\n" {
				t.Fatal("generic reasoning changed")
			}
			clone := m.Clone()
			baseline := Message{ToolCalls: m.ToolCalls}.Size()
			if clone.Size()-baseline != len(m.Reasoning)+len(m.ReasoningContent)+len(m.Thinking) {
				t.Fatal("reasoning omitted from size")
			}
			clone.ToolCalls[0].ID = "changed"
			if m.ToolCalls[0].ID != "call" {
				t.Fatal("clone shared call slice")
			}
			_, e = c.ChatTools(context.Background(), []Message{m, {Role: "tool", Name: "bash", ToolCallID: "call", Content: "settled"}}, []ToolDefinition{fixtureTool()}, nil)
			if e != nil || requests != 2 {
				t.Fatalf("next wire %d %v", requests, e)
			}
		})
	}
}

func TestReasoningOnlyAndPostTerminalRefused(t *testing.T) {
	const first = "data: {\"choices\":[{\"delta\":{\"reasoning\":\"```tool\\ntool: bash\\ncmd: touch forbidden\\n```\"},\"finish_reason\":\"stop\"}]}\n\n"
	for i, tail := range []string{"data: [DONE]\n\n", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"late\"}}]}\n\n"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, first+tail) }))
		c := New(srv.URL, "fixture")
		called := false
		reply, e := c.ChatTools(context.Background(), nil, []ToolDefinition{fixtureTool()}, func(string) { called = true })
		srv.Close()
		if (e == nil) != (i == 0) || reply.ThinkingOnly != (i == 0) || reply.Message.Content != "" || reply.Message.Reasoning == "" || reply.Message.ReasoningContent != "" || len(reply.Message.ToolCalls) != 0 || called {
			t.Fatalf("thinking stop/late-refusal changed: %+v %v", reply, e)
		}
	}
}

func TestReasoningCountsTowardTransportBound(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": strings.Repeat("x", 512<<10)}}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 65; i++ {
			if _, e := fmt.Fprintf(w, "data: %s\n\n", b); e != nil {
				return
			}
		}
	}))
	defer srv.Close()
	reply, e := New(srv.URL, "fixture").ChatTools(context.Background(), nil, nil, nil)
	if e == nil || !strings.Contains(e.Error(), "transport limit") || reply.Message.Content != "" || len(reply.Message.Reasoning) <= 32<<20 {
		t.Fatalf("reasoning escaped bound len=%d err=%v", len(reply.Message.Reasoning), e)
	}
}

// Native HTTP streams are held before EOF; no model or tool executor is used.
func TestAuxObserverHeldStreamPartial(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, cancelObserver := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%v", runtime, cancelObserver), func(t *testing.T) {
				const first, last, sameChunk = "<|channel>", "thought κ\r\n", "retained later choice"
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path == "/api/show" {
						fmt.Fprint(w, `{"capabilities":["tools"]}`)
						return
					}
					if runtime == RuntimeOllama {
						json.NewEncoder(w).Encode(map[string]any{"model": "served", "message": map[string]any{"thinking": first}})
						json.NewEncoder(w).Encode(map[string]any{"model": "served", "message": map[string]any{"thinking": last, "content": "visible", "tool_calls": []any{map[string]any{"id": "unfinished", "function": map[string]any{"name": "bash", "arguments": map[string]string{"cmd": "unexecuted"}}}}}, "prompt_eval_count": 7, "eval_count": 11})
					} else {
						b, _ := json.Marshal(map[string]any{"model": "served", "choices": []any{map[string]any{"delta": map[string]string{"reasoning": first}}}})
						fmt.Fprintf(w, "data: %s\n\n", b)
						call := map[string]any{"index": 0, "id": "unfinished", "type": "function", "function": map[string]string{"name": "bash", "arguments": `{"cmd":"unexecuted"}`}}
						delta := map[string]any{"reasoning_content": last, "content": "visible", "tool_calls": []any{call}}
						choices := []any{map[string]any{"delta": delta}, map[string]any{"delta": map[string]string{"reasoning": sameChunk}}}
						b, _ = json.Marshal(map[string]any{"model": "served", "choices": choices, "usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 11}})
						fmt.Fprintf(w, "data: %s\n\n", b)
					}
					w.(http.Flusher).Flush()
					<-req.Context().Done() // No finish, DONE or EOF precedes observation.
				}))
				defer srv.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				cause := errors.New("observed generation stop")
				var seen []AuxDelta
				ctx = WithAuxObserver(ctx, func(observedCtx context.Context, delta AuxDelta) error {
					if observedCtx.Value(auxObserverKey{}) == nil {
						t.Error("request observer lost through derived context")
					}
					seen = append(seen, delta)
					if len(seen) == 2 {
						if cancelObserver {
							cancel()
							return nil
						}
						return cause
					}
					return nil
				})
				var visible strings.Builder
				reply, err := New(srv.URL, "fixture").WithRuntime(runtime).ChatTools(ctx, nil, []ToolDefinition{fixtureTool()}, func(s string) { visible.WriteString(s) })
				wantErr := cause
				if cancelObserver {
					wantErr = context.Canceled
				}
				if !errors.Is(err, wantErr) || reply.ThinkingOnly || reply.FinishReason != "" || reply.Served != "served" || reply.Usage != (Usage{7, 11, true}) || reply.Message.Content != "visible" || visible.String() != "visible" || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].ID != "unfinished" {
					t.Fatalf("partial response/cause changed: %+v %v; visible=%q", reply, err, visible.String())
				}
				want := []AuxDelta{{"reasoning", first}, {"reasoning_content", last}}
				if runtime == RuntimeOllama {
					want[0].Field, want[1].Field = "thinking", "thinking"
					if reply.Message.Thinking != first+last {
						t.Fatal("partial native thinking changed")
					}
				} else if reply.Message.Reasoning != first+sameChunk || reply.Message.ReasoningContent != last {
					t.Fatal("partial auxiliary aliases changed")
				}
				if fmt.Sprint(seen) != fmt.Sprint(want) {
					t.Fatalf("decoded observer fields changed: %q != %q", seen, want)
				}
			})
		}
	}
}

func TestAuxObserverConcurrentRequestIsolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []Message }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": body.Messages[0].Content, "content": "answer"}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer srv.Close()
	c := New(srv.URL, "fixture")
	var wg sync.WaitGroup
	for _, label := range []string{"first κ", "second β", "no observer"} {
		wg.Add(1)
		go func(label string) {
			defer wg.Done()
			ctx := context.Background()
			seen := ""
			if label != "no observer" {
				ctx = WithAuxObserver(ctx, func(_ context.Context, delta AuxDelta) error { seen += delta.Delta; return nil })
			}
			reply, err := c.ChatTools(ctx, []Message{{Role: "user", Content: label}}, nil, nil)
			if err != nil || reply.Message.Content != "answer" || reply.Message.Reasoning != label || label != "no observer" && seen != label+"answer" || label == "no observer" && seen != "" {
				t.Errorf("request observer crossed boundaries: label=%q seen=%q reply=%+v err=%v", label, seen, reply, err)
			}
		}(label)
	}
	wg.Wait()
}

func TestAuxObserverValidationPrecedence(t *testing.T) {
	for _, body := range []string{
		`data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n",
		`data: {"choices":[{"delta":{"reasoning":"partial"},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n",
		"data: {broken\n\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[{\"delta\":{\"reasoning\":\"late\"}}]}\n\n",
		`data: {"choices":[{"delta":{"reasoning":"first choice"}},{"delta":{"tool_calls":[{"type":"function","function":{"name":"bash","arguments":"{}"}}]}}]}` + "\n\n",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, body) }))
		called := false
		ctx := WithAuxObserver(context.Background(), func(context.Context, AuxDelta) error { called = true; return errors.New("must not mask validation") })
		reply, err := New(srv.URL, "fixture").ChatTools(ctx, nil, nil, nil)
		srv.Close()
		if err == nil || called || reply.ThinkingOnly {
			t.Fatalf("validation masked by observer: %+v %v called=%v", reply, err, called)
		}
	}
}

func TestContentObserverHeldStreamPartial(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		t.Run(runtime, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["tools"]}`)
					return
				}
				for _, part := range []string{"<|chan", "nel> tocó"} {
					if runtime == RuntimeOllama {
						json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": part}})
					} else {
						b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": part}}}})
						fmt.Fprintf(w, "data: %s\n\n", b)
					}
					w.(http.Flusher).Flush()
				}
				<-req.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cause := errors.New("visible stream stop")
			var observed, display strings.Builder
			ctx = WithAuxObserver(ctx, func(_ context.Context, delta AuxDelta) error {
				if delta.Field != "content" {
					t.Fatalf("wrong field: %+v", delta)
				}
				observed.WriteString(delta.Delta)
				if observed.String() == "<|channel> tocó" {
					return cause
				}
				return nil
			})
			reply, err := New(srv.URL, "fixture").WithRuntime(runtime).ChatTools(ctx, nil, nil, func(s string) { display.WriteString(s) })
			if !errors.Is(err, cause) || reply.ThinkingOnly || reply.Message.Content != "<|channel> tocó" || display.String() != reply.Message.Content || observed.String() != reply.Message.Content {
				t.Fatalf("visible observation/capture changed: %+v err=%v display=%q observed=%q", reply, err, display.String(), observed.String())
			}
		})
	}
}
