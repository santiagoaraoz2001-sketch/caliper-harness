package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

type consumptionSink struct {
	recordSink
	refuse bool
}

func (s *consumptionSink) Append(ctx context.Context, id, kind, text string) error {
	if kind == "consumed" && s.refuse {
		return errors.New("actual consumption persistence failure")
	}
	return s.recordSink.Append(ctx, id, kind, text)
}

type consumptionNative struct{ scriptModel }

func (m *consumptionNative) ChatTools(ctx context.Context, messages []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, messages)
	return llm.Reply{Message: llm.Message{Role: "assistant", Content: "observed response"}, FinishReason: "stop"}, m.err
}

func TestRunnerConsumptionIsDurableOnlyAfterSuccessfulRequest(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"success", "failed", "cancelled", "persistence"} {
			t.Run(map[bool]string{false: "text", true: "native"}[native]+"/"+mode, func(t *testing.T) {
				sink := &consumptionSink{refuse: mode == "persistence"}
				var cause error
				if mode == "failed" {
					cause = errors.New("actual request failure")
				}
				if mode == "cancelled" {
					cause = context.Canceled
				}
				var model Model = &scriptModel{replies: []string{"observed response"}, err: cause}
				if native {
					model = &consumptionNative{scriptModel: scriptModel{err: cause}}
				}
				c := session.Continuation{Version: 1}
				c.Add("intent", "user", "Preserve current scope", false)
				c.Add("output", "user", "original unread source", true)
				r := &Runner{Prompts: prompts.Builtin(), Model: model, Registry: tool.NewRegistry(), Session: sink, SessionID: "actual", Continuation: c, MaxSteps: 1}
				_, _, err := r.Run(t.Context(), "Summarize the actual source")
				consumed := 0
				for _, kind := range sink.kinds {
					if kind == "consumed" {
						consumed++
					}
				}
				want := mode == "success"
				if (err == nil) != want || consumed != map[bool]int{false: 0, true: 1}[want] {
					t.Fatal("success/receipt mismatch", err, consumed)
				}
				found := false
				for _, entry := range r.Continuation.Entries {
					if entry.Message.Content == "original unread source" {
						found = true
						if entry.Unread == want {
							t.Fatal("wrong consumed state", mode)
						}
					}
				}
				if !found {
					t.Fatal("source lost")
				}
			})
		}
	}
}

func TestActualTextAskArchiveConsumptionAndInterruptedRestore(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "ask", Params: []tool.Param{{Name: "question", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: "Preserve original identity; teal only for navigation."}, nil
	}})
	sink := &recordSink{}
	model := &scriptModel{replies: []string{"```tool\ntool: ask\nquestion: Which aesthetic?\n```", "I will preserve that identity."}}
	r := &Runner{Prompts: prompts.Builtin(), Model: model, Registry: reg, Session: sink, SessionID: "ask", MaxSteps: 2}
	if _, _, err := r.Run(t.Context(), "Ask for my aesthetic before choosing it"); err != nil {
		t.Fatal(err)
	}
	events := []session.Event{}
	cut := -1
	answer := ""
	for i, kind := range sink.kinds {
		if kind == "continuation" {
			continue
		} // Test actual raw replay, not the final snapshot.
		if kind == "consumed" {
			cut = len(events)
		}
		if kind == "ask_answer" {
			answer = sink.texts[i]
		}
		events = append(events, session.Event{Kind: kind, Text: sink.texts[i]})
	}
	if cut < 0 {
		t.Fatal("actual Ask result was never consumed")
	}
	if strings.Count(answer, "Preserve original identity; teal only for navigation.") != 1 {
		t.Fatal("actual answer receipt lost original bytes", answer)
	}
	for _, prefix := range []bool{true, false} {
		tail := events
		if prefix {
			tail = events[:cut]
		}
		c, _, err := session.Restore(tail, 10000)
		if err != nil {
			t.Fatal("actual text Ask archive cannot restore", err)
		}
		answers := 0
		for i, entry := range c.Entries {
			if entry.Kind != "answer" {
				continue
			}
			answers++
			if entry.Unread != prefix || !c.Protected(0)[i] || entry.Message.Content != answer {
				t.Fatal("Ask raw replay lost unread/protected identity", prefix, entry)
			}
		}
		if answers != 1 {
			t.Fatal("Ask result duplicated or lost", answers)
		}
	}
}

func TestActualTextSchemaAndFailureConsumptionPreservesProtection(t *testing.T) {
	for _, kind := range []string{"schema", "failure"} {
		t.Run(kind, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Register(tool.Definition{Name: "probe", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
				return tool.Result{}, errors.New("actual probe failure")
			}})
			call := "```tool\ntool: probe\n```"
			if kind == "failure" {
				call = "```tool\ntool: probe\nvalue: actual\n```"
			}
			sink := &recordSink{}
			r := &Runner{Prompts: prompts.Builtin(), Model: &scriptModel{replies: []string{call, "I have read the result."}}, Registry: reg, Session: sink, SessionID: "typed", MaxSteps: 2}
			if _, _, err := r.Run(t.Context(), "Inspect the probe"); err != nil {
				t.Fatal(err)
			}
			var events []session.Event
			cut := -1
			body := ""
			for i, k := range sink.kinds {
				if k == "continuation" {
					continue
				}
				if k == "result" {
					body = sink.texts[i]
				}
				if k == "consumed" {
					cut = len(events)
				}
				events = append(events, session.Event{Kind: k, Text: sink.texts[i]})
			}
			if cut < 0 || body == "" {
				t.Fatal("missing actual result/consumption")
			}
			for _, before := range []bool{true, false} {
				tail := events
				if before {
					tail = events[:cut]
				}
				restored, _, err := session.Restore(tail, 10000)
				if err != nil {
					t.Fatal(err)
				}
				matches := 0
				for i, e := range restored.Entries {
					if e.Message.Content != body {
						continue
					}
					matches++
					wantKind := kind
					if before {
						wantKind = "output"
					} // Untyped historical event stays unread.
					if e.Kind != wantKind || e.Unread != before || !restored.Protected(0)[i] {
						t.Fatalf("lost result provenance/protection: before=%v entry=%+v", before, e)
					}
					if !before {
						found := false
						for _, live := range r.Continuation.Entries {
							if live.Message.Content == body {
								found = true
								if live.Kind != e.Kind || live.Unread != e.Unread || live.Message.Role != e.Message.Role {
									t.Fatal("live/tail mismatch", live, e)
								}
							}
						}
						if !found {
							t.Fatal("no matching live result")
						}
					}
				}
				if matches != 1 {
					t.Fatal("result duplicated/lost", matches)
				}
			}
		})
	}
}
