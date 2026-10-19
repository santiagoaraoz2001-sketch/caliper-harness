package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

// The same normal completion advances through every serving route without a
// custom error envelope, added user message, or execution of reasoning text.
func TestThinkingContinuationAcrossServers(t *testing.T) {
	for _, runtime := range []string{llm.RuntimeOpenAI, llm.RuntimeMLX, llm.RuntimeOllama} {
		t.Run(runtime, func(t *testing.T) {
			requests, effects := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["tools"]}`)
					return
				}
				var body struct {
					Messages []struct {
						Role, Content, Reasoning, Thinking string
						ReasoningContent                   string `json:"reasoning_content"`
					}
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if requests == 1 {
					users, thoughts := 0, 0
					for _, m := range body.Messages {
						if m.Role == "user" {
							users++
							if m.Content != "inspect README.md" {
								t.Errorf("extra prompt: %q", m.Content)
							}
						}
						if m.Reasoning == "I will inspect README.md." || m.ReasoningContent == "I will inspect README.md." || m.Thinking == "I will inspect README.md." {
							thoughts++
						}
					}
					if users != 1 || thoughts != 1 || effects != 0 {
						t.Errorf("history/effects: users=%d thoughts=%d effects=%d", users, thoughts, effects)
					}
				}
				if requests > 2 {
					t.Error("unexpected extra request")
					http.Error(w, "unexpected", 500)
					return
				}
				if runtime == llm.RuntimeOllama {
					frames := []string{
						`{"message":{"thinking":"I will inspect README.md."},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`,
						`{"message":{"tool_calls":[{"id":"read1","function":{"name":"read","arguments":{"path":"README.md"}}}]},"done":true,"done_reason":"stop"}`,
						`{"message":{"content":"Inspected."},"done":true,"done_reason":"stop"}`,
					}
					fmt.Fprintln(w, frames[requests])
				} else {
					frames := []string{
						`{"choices":[{"delta":{"reasoning":"I will inspect README.md."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
						`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"read1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
						`{"choices":[{"delta":{"content":"Inspected."},"finish_reason":"stop"}]}`,
					}
					frame := frames[requests]
					if runtime == llm.RuntimeMLX {
						frame = strings.ReplaceAll(frame, `"reasoning":`, `"reasoning_content":`)
					}
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
				}
				requests++
			}))
			defer server.Close()
			registry := tool.NewRegistry()
			registry.Register(tool.Definition{Name: "read", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}})
			runner := agent.Runner{Model: llm.New(server.URL, "fixture").WithRuntime(runtime), Registry: registry, MaxSteps: 4, Out: &bytes.Buffer{}}
			runner.Exec = func(_ context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
				effects++
				if d.Name != "read" || a["path"] != "README.md" {
					t.Errorf("unexpected execution: %v %v", d.Name, a)
				}
				return tool.Result{Text: "project README"}, nil
			}
			_, _, err := runner.Run(t.Context(), "inspect README.md")
			if err != nil || requests != 3 || effects != 1 || runner.Outcome.Status != outcome.Completed {
				t.Fatalf("outcome=%+v err=%v requests=%d effects=%d", runner.Outcome, err, requests, effects)
			}
		})
	}
}

type cancelAfterThinking struct{ cancel context.CancelFunc }

func (s cancelAfterThinking) Append(_ context.Context, _, kind, text string) error {
	if kind == "note" && strings.Contains(text, "continuing from retained context") {
		s.cancel()
	}
	return nil
}
func TestThinkingContinuationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, _, calls := thinkingRunner(t, thinking227, cancelAfterThinking{cancel})
	_, _, err := r.Run(ctx, "inspect the project")
	if !errors.Is(err, context.Canceled) || *calls != 1 || r.Outcome.Status != outcome.Cancelled {
		t.Fatalf("%+v err=%v calls=%d", r.Outcome, err, *calls)
	}
}
