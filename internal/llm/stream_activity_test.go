package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// Hold an actual HTTP response after reasoning, before its terminal event.
// Activity must arrive during generation without turning reasoning into an
// answer or admitting an unfinished call.
func TestStreamActivityBeforeCompletionPreservesReply(t *testing.T) {
	for _, runtime := range []string{RuntimeOpenAI, RuntimeMLX, RuntimeOllama} {
		for _, cancelEarly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%v", runtime, cancelEarly), func(t *testing.T) {
				release := make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/show" {
						fmt.Fprint(w, `{"capabilities":["tools"]}`)
						return
					}
					if runtime == RuntimeOllama {
						fmt.Fprintln(w, `{"message":{"thinking":"inspect the source"}}`)
					} else {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"inspect the source\"}}]}\n\n: keepalive\n\n")
					}
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
						return
					case <-release:
					}
					if runtime == RuntimeOllama {
						fmt.Fprintln(w, `{"message":{"tool_calls":[{"id":"call","function":{"name":"bash","arguments":{}}}]},"done":true,"done_reason":"stop"}`)
					} else {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					}
				}))
				defer srv.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				stages := make(chan string, 10)
				result := make(chan struct {
					reply Reply
					err   error
				}, 1)
				visible := ""
				c := New(srv.URL, "fixture").WithRuntime(runtime).WithStreamObserver(func(stage string) { stages <- stage })
				go func() {
					reply, err := c.WithoutThinking().ChatTools(ctx, nil, []ToolDefinition{fixtureTool()}, func(s string) { visible += s })
					result <- struct {
						reply Reply
						err   error
					}{reply, err}
				}()
				for _, want := range []string{"waiting", "thinking"} {
					select {
					case got := <-stages:
						if got != want {
							t.Fatalf("activity before completion: got %q want %q", got, want)
						}
					case <-ctx.Done():
						t.Fatal("no activity before completion")
					}
				}
				select {
				case <-result:
					t.Fatal("unfinished response admitted before terminal")
				default:
				}
				if cancelEarly {
					cancel()
				} else {
					close(release)
				}
				got := <-result
				if visible != "" || got.reply.Message.Content != "" ||
					got.reply.Message.ReasoningContent+got.reply.Message.Thinking != "inspect the source" {
					t.Fatalf("reasoning changed or leaked: %+v visible=%q", got.reply, visible)
				}
				wantTail := []string{"tool", ""}
				if cancelEarly {
					wantTail = []string{""}
					if !errors.Is(got.err, context.Canceled) || len(got.reply.Message.ToolCalls) != 0 {
						t.Fatalf("cancelled partial changed: %+v %v", got.reply, got.err)
					}
				} else if got.err != nil || len(got.reply.Message.ToolCalls) != 1 || got.reply.Message.ToolCalls[0].ID != "call" {
					t.Fatalf("completed call changed: %+v %v", got.reply, got.err)
				}
				var tail []string
				for len(stages) > 0 {
					tail = append(tail, <-stages)
				}
				if !reflect.DeepEqual(tail, wantTail) {
					t.Fatalf("activity tail %q want %q", tail, wantTail)
				}
			})
		}
	}
}

func TestStreamActivityKeepaliveContentAndFailure(t *testing.T) {
	for _, body := range []string{
		": heartbeat\n\n: heartbeat\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		": heartbeat\n\ndata: invalid\n\n",
	} {
		c, _ := stubClient(t, 200, body)
		var stages []string
		c.WithStreamObserver(func(s string) { stages = append(stages, s) })
		reply, _, _, err := c.ChatStream(context.Background(), nil, nil)
		want := []string{"waiting", "active", "content", ""}
		if reply == "" {
			want = []string{"waiting", "active", ""}
			if err == nil {
				t.Fatal("malformed response became successful")
			}
		} else if err != nil || reply != "hello" {
			t.Fatalf("answer changed: %q %v", reply, err)
		}
		if !reflect.DeepEqual(stages, want) {
			t.Fatalf("activity %q want %q", stages, want)
		}
	}
}
