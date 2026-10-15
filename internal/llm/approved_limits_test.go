package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func approvedPlainTimeout(c *Client, d time.Duration) {
	m := reflect.ValueOf(c).MethodByName("WithPlainTimeout")
	if m.IsValid() {
		m.Call([]reflect.Value{reflect.ValueOf(d)})
	}
}
func TestApprovedPlainTimeoutAndCapturedBypass(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprint(capture), func(t *testing.T) {
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				close(entered)
				select {
				case <-r.Context().Done():
					close(cancelled)
					return
				case <-time.After(100 * time.Millisecond):
				}
				if capture {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`)
				}
			}))
			defer srv.Close()
			c := New(srv.URL, "fixture")
			approvedPlainTimeout(c, 20*time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if capture {
				reply, e := c.ChatTools(ctx, []Message{{Role: "user", Content: "answer"}}, nil, nil)
				if e != nil || reply.Message.Content != "answer" {
					t.Fatalf("captured bypass reply=%+v e=%v", reply, e)
				}
			} else {
				_, _, _, e := c.Chat(ctx, []Message{{Role: "user", Content: "answer"}})
				if e == nil || !errors.Is(e, context.DeadlineExceeded) {
					t.Fatalf("plain timeout=%v", e)
				}
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("actual HTTP request not cancelled")
				}
			}
			select {
			case <-entered:
			default:
				t.Fatal("no actual request")
			}
		})
	}
}
