package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

type steeringQueue struct {
	mu        sync.Mutex
	notes     []string
	interrupt context.CancelFunc
}

func (q *steeringQueue) pop() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.notes) == 0 {
		return ""
	}
	n := q.notes[0]
	q.notes = q.notes[1:]
	return n
}
func (q *steeringQueue) pending() bool { q.mu.Lock(); defer q.mu.Unlock(); return len(q.notes) != 0 }
func (q *steeringQueue) generating(c context.CancelFunc) {
	q.mu.Lock()
	q.interrupt = c
	pending := len(q.notes) != 0
	q.mu.Unlock()
	if c != nil && pending {
		c()
	}
}
func (q *steeringQueue) push(s string) {
	q.mu.Lock()
	q.notes = append(q.notes, s)
	c := q.interrupt
	q.mu.Unlock()
	if c != nil {
		c()
	}
}

func TestUserCommentInterruptsOnlyGenerationAndRetainsContext(t *testing.T) {
	for _, prefill := range []bool{true, false} {
		t.Run(fmt.Sprintf("prefill=%t", prefill), func(t *testing.T) {
			entered, received, cancelled := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
			var requests [][]llm.Message
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Messages []llm.Message
					Prefix   json.RawMessage `json:"response_prefix"`
				}
				json.NewDecoder(req.Body).Decode(&body)
				mu.Lock()
				requests = append(requests, body.Messages)
				n := len(requests)
				mu.Unlock()
				if n == 1 {
					if !prefill {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"unfinished answer\",\"reasoning\":\"exact prior thought\",\"tool_calls\":[{\"index\":0,\"id\":\"abandoned\",\"type\":\"function\",\"function\":{\"name\":\"effect\",\"arguments\":\"{\\\"name\\\":\"}}]}}]}\n\n")
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-req.Context().Done()
					close(cancelled)
					return
				}
				if len(body.Prefix) != 0 {
					t.Error("steering reused the abandoned literal prefix")
				}
				if n == 2 {
					last := body.Messages[len(body.Messages)-1]
					if last.Role != "user" || last.Content != "USER COMMENT: changed objective" {
						t.Errorf("comment not a real user message: %+v", last)
					}
					if !prefill {
						prior := body.Messages[len(body.Messages)-2]
						if !strings.HasPrefix(prior.Content, "unfinished answer") || !strings.Contains(prior.Content, "abandoned") || !strings.Contains(prior.Content, "none executed") || prior.Reasoning != "exact prior thought" || len(prior.ToolCalls) != 0 {
							t.Errorf("received partial context lost or executable: %+v", prior)
						}
					}
					writeContinuationChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "new-effect", "type": "function", "function": map[string]string{"name": "effect", "arguments": `{"name":"redirected"}`}}}}, "tool_calls", nil, 10, 1)
				} else {
					writeContinuationChunk(w, map[string]any{"content": "redirected done"}, "stop", nil, 10, 1)
				}
			}))
			defer srv.Close()
			reg := tool.NewRegistry()
			effects := 0
			reg.Register(tool.Definition{Name: "effect", Params: []tool.Param{{Name: "name", Type: tool.TypeString, Required: true}}, Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if ctx.Err() != nil || args["name"] != "redirected" {
					t.Error("effect inherited generation cancellation or old args")
				}
				effects++
				return tool.Result{Text: "settled actual effect"}, nil
			}})
			queue := &steeringQueue{}
			r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: reg, Session: &nativeRecordingSink{}, SessionID: "steer", MaxSteps: 2, Btw: queue.pop, GenerationInterrupt: queue.generating, UserCommentPending: queue.pending, OnToken: func(string) {
				select {
				case received <- struct{}{}:
				default:
				}
			}}
			done := make(chan error, 1)
			go func() { _, _, err := r.Run(t.Context(), "original objective"); done <- err }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("fixture request did not enter")
			}
			if !prefill {
				select {
				case <-received:
				case <-time.After(2 * time.Second):
					t.Fatal("partial delta not received")
				}
			}
			queue.push("changed objective")
			select {
			case err := <-done:
				if err != nil || r.Outcome.Status != outcome.Completed || effects != 1 {
					t.Fatalf("steering failed: %v %+v effects=%d", err, r.Outcome, effects)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("/btw did not interrupt the in-flight generation")
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("old generation HTTP request not cancelled")
			}
			mu.Lock()
			n := len(requests)
			mu.Unlock()
			if n != 3 {
				t.Fatalf("unexpected requests %d", n)
			}
		})
	}
}

func TestExplicitCancellationNeverStartsAContinuation(t *testing.T) {
	entered := make(chan struct{})
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"partial thought\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		<-req.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: tool.NewRegistry(), Session: &nativeRecordingSink{}, SessionID: "cancel", MaxSteps: 2}
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(ctx, "original objective"); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil || r.Outcome.Status != outcome.Cancelled || requests != 1 {
			t.Fatalf("explicit stop covertly resumed: %v %+v requests=%d", err, r.Outcome, requests)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit cancellation did not settle")
	}
}

func TestTransportFailureAfterLengthDoesNotContinue(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"original partial\"},\"finish_reason\":\"length\"}],\"response_prefix\":{\"tokens\":[1],\"raw_chunks\":[\"original partial\"],\"initial_state\":\"reasoning\",\"prompt_sha256\":\""+strings.Repeat("a", 64)+"\",\"tool_index\":0}}\n\ndata: broken-json\n\n")
	}))
	defer srv.Close()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: tool.NewRegistry(), Session: &nativeRecordingSink{}, SessionID: "broken", MaxSteps: 2}
	_, _, err := r.Run(t.Context(), "original objective")
	if err == nil || requests != 1 || r.Outcome.Status != outcome.Failed {
		t.Fatalf("broken transport resumed: %v %+v requests=%d", err, r.Outcome, requests)
	}
}

func TestUserCommentWaitsForRunningEffectAndRefusesUnstartedArrayTail(t *testing.T) {
	queue := &steeringQueue{}
	started, release := make(chan struct{}), make(chan struct{})
	reg := tool.NewRegistry()
	effects := []string{}
	reg.Register(tool.Definition{Name: "effect", Params: []tool.Param{{Name: "name", Type: tool.TypeString, Required: true}}, Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
		name := args["name"].(string)
		effects = append(effects, name)
		if name == "first" {
			close(started)
			<-release
			if ctx.Err() != nil {
				t.Error("/btw cancelled a running effect")
			}
		}
		return tool.Result{Text: "actual effect settled " + name}, nil
	}})
	model := &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "first-id", Name: "effect", Arguments: `{"name":"first"}`}, {ID: "tail-id", Name: "effect", Arguments: `{"name":"must-not-run"}`}}}, FinishReason: "tool_calls"}}}
	r := &agent.Runner{Model: model, Registry: reg, Session: &nativeRecordingSink{}, SessionID: "effects", MaxSteps: 2, Btw: queue.pop, GenerationInterrupt: queue.generating, UserCommentPending: queue.pending}
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(t.Context(), "original work"); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("actual first effect did not start")
	}
	queue.push("change before next effect")
	close(release)
	select {
	case err := <-done:
		if err != nil || r.Outcome.Status != outcome.Completed {
			t.Fatalf("steering failed after effect settlement: %v %+v", err, r.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("settled effect did not permit steering")
	}
	if len(effects) != 1 || effects[0] != "first" || len(model.seen) != 2 {
		t.Fatalf("an unstarted effect ran or was charged: effects=%v requests=%d", effects, len(model.seen))
	}
	last := model.seen[1]
	if last[len(last)-1].Role != "user" || last[len(last)-1].Content != "USER COMMENT: change before next effect" {
		t.Fatal("comment did not follow the correlated settled receipts")
	}
	for _, entry := range r.Continuation.Entries {
		if entry.Message.ToolCallID == "tail-id" && (entry.Kind != "failure" || !strings.Contains(entry.Message.Content, "not executed")) {
			t.Fatalf("tail lacked honest refusal receipt: %+v", entry)
		}
	}
}

func TestUserCommentRetainsEarlierLengthPrefixDuringFreshPrefill(t *testing.T) {
	queue := &steeringQueue{}
	entered := make(chan struct{})
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		var body struct {
			Messages []llm.Message
			Prefix   json.RawMessage `json:"response_prefix"`
		}
		json.NewDecoder(req.Body).Decode(&body)
		switch requests {
		case 1:
			writeContinuationChunk(w, map[string]any{"content": "original unfinished answer", "reasoning": "original unfinished thought"}, "length", map[string]any{"tokens": []int{1}, "raw_chunks": []string{"original raw framing"}, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0}, 10, 1)
		case 2:
			if len(body.Prefix) == 0 {
				t.Error("second physical request was not literal prefill")
			}
			close(entered)
			<-req.Context().Done()
		case 3:
			prior, last := body.Messages[len(body.Messages)-2], body.Messages[len(body.Messages)-1]
			if prior.Content != "original unfinished answer" || prior.Reasoning != "original unfinished thought" || last.Role != "user" || last.Content != "USER COMMENT: redirect unfinished work" || len(body.Prefix) != 0 {
				t.Errorf("old prefix lost during empty fresh prefill: %+v %+v", prior, last)
			}
			writeContinuationChunk(w, map[string]any{"content": "redirected"}, "stop", nil, 11, 1)
		default:
			t.Error("unexpected repeat after steering")
		}
	}))
	defer srv.Close()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: tool.NewRegistry(), Session: &nativeRecordingSink{}, SessionID: "prior-prefix", MaxSteps: 1, Btw: queue.pop, GenerationInterrupt: queue.generating, UserCommentPending: queue.pending}
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(t.Context(), "original objective"); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("length did not start the literal prefill")
	}
	queue.push("redirect unfinished work")
	select {
	case err := <-done:
		if err != nil || r.Outcome.Status != outcome.Completed || requests != 3 {
			t.Fatalf("pending prefix steering failed: %v %+v requests=%d", err, r.Outcome, requests)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prefix steering did not settle")
	}
}
