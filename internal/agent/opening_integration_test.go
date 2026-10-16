package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestRepairedNativeCallRetainsPreflightAndAuthorization(t *testing.T) {
	for _, mode := range []string{"plan-array", "denied", "mixed", "partial", "log-failure"} {
		t.Run(mode, func(t *testing.T) {
			r := repairRunner()
			r.Continuation = session.Continuation{Version: 1}
			r.Continuation.Add("intent", "user", "inspect the source", false)
			judged, effects, approvals := 0, 0, 0
			target := "read"
			message := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "one", Name: "inspect", Arguments: `{"path":"a.go"}`}}}
			switch mode {
			case "plan-array":
				target = "plan"
				message.ToolCalls = []llm.ToolCall{{ID: "one", Name: "start_planning", Arguments: `{}`}, {ID: "two", Name: "read", Arguments: `{"path":"a.go"}`}}
			case "mixed":
				message.Content = "```tool\ntool: read\npath: a.go\n```"
			case "partial":
				message.ToolCalls[0].Arguments = `{"path":`
			}
			r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				judged++
				return chooseRepair(target, .9)(ctx, req)
			}
			exec := func(_ context.Context, def tool.Definition, _ tool.Args) (tool.Result, error) {
				approvals++
				if def.Name != "read" {
					t.Fatal(def.Name)
				}
				return tool.Result{Blocked: true, Text: "denied"}, nil
			}
			_, _, err := r.nativeStep(context.Background(), message, exec, func(kind, _ string) error {
				if mode == "log-failure" && kind == "tool-repair" {
					return errors.New("cannot save selection")
				}
				return nil
			}, nil, func(string, bool, string) string { return "" })
			if effects != 0 {
				t.Fatalf("effects %d", effects)
			}
			if mode == "denied" && (judged != 1 || approvals != 1) {
				t.Fatalf("judgments %d approvals %d", judged, approvals)
			}
			if mode != "denied" && approvals != 0 {
				t.Fatalf("unexpected approval %d", approvals)
			}
			if (mode == "mixed" || mode == "partial") && judged != 0 {
				t.Fatalf("ambiguous reply judged %d times", judged)
			}
			if mode == "plan-array" && judged != 1 {
				t.Fatalf("expected actual repaired array preflight, judged %d", judged)
			}
			if mode == "log-failure" && err == nil {
				t.Fatal("selection recording failure swallowed")
			}
		})
	}
}

func TestInvocationNoteKeepsStructuredReceiptAndFailure(t *testing.T) {
	text := annotateInvocationResult(`tool result:
{"data":{"id":"actual"},"effects":[{"status":"applied"}],"_harness_call_note":"owner value"}`, "ignored query")
	var object map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "tool result:\n")), &object); err != nil {
		t.Fatal(err, text)
	}
	if object["data"].(map[string]any)["id"] != "actual" || object["_harness_call_note"] != "owner value" || object["_harness_call_note_2"] != "ignored query" {
		t.Fatal(object)
	}
	if got := annotateInvocationResult("tool error: denied", "reconstructed"); !strings.HasPrefix(got, "tool error: denied") {
		t.Fatal(got)
	}
}

func openingRunner(t *testing.T, name string, params []tool.Param, run func(tool.Args)) *Runner {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: name, Opening: true, Params: params, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
		run(a)
		return tool.Result{Text: "opened"}, nil
	}})
	r := &Runner{Registry: reg, Prompts: prompts.Builtin(), Continuation: session.Continuation{Version: 1}}
	r.Continuation.Add("intent", "user", "open the requested tool", false)
	return r
}

func TestOpeningNativePreservesOriginalAndDisclosesIgnoredFields(t *testing.T) {
	for _, raw := range []string{`{"query":""}`, `{"query":"x"}`, `{"query":"test"}`} {
		t.Run(raw, func(t *testing.T) {
			calls := 0
			r := openingRunner(t, "plan", nil, func(a tool.Args) {
				calls++
				if len(a) != 0 {
					t.Fatal(a)
				}
			})
			message := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "open", Name: "plan", Arguments: raw}}}
			obs, _, err := r.nativeStep(context.Background(), message, Direct, func(string, string) error { return nil }, nil, func(string, bool, string) string { return "" })
			if err != nil || calls != 1 || !strings.Contains(obs, "not saved") || !strings.Contains(obs, "query") {
				t.Fatalf("calls %d obs %s err %v", calls, obs, err)
			}
			if r.Continuation.Entries[1].Message.ToolCalls[0].Arguments != raw || message.ToolCalls[0].Arguments != raw {
				t.Fatal("original invocation was rewritten")
			}
			if err := r.Continuation.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpeningTextKeepsExplicitTopic(t *testing.T) {
	r := openingRunner(t, "help", []tool.Param{{Name: "topic", Type: tool.TypeString}}, func(a tool.Args) {
		if a["topic"] != "schema.read" || len(a) != 1 {
			t.Fatal(a)
		}
	})
	call := tool.Call{Name: "help", Slots: map[string]string{"topic": "schema.read", "query": "unused"}}
	obs, _, ok := r.handleCall(context.Background(), call, "", Direct)
	if !ok || !strings.Contains(obs, "not saved") || call.Slots["query"] != "unused" {
		t.Fatal(obs, ok, call)
	}
}

func TestOpeningNativeRefusesUnsafeArrayAndRecordingFailure(t *testing.T) {
	for _, mode := range []string{"mixed", "malformed", "duplicate", "array", "recording"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			r := openingRunner(t, "plan", nil, func(tool.Args) { calls++ })
			message := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "open", Name: "plan", Arguments: `{"query":"x"}`}}}
			switch mode {
			case "mixed":
				message.Content = "```tool\ntool: plan\n```"
			case "malformed":
				message.ToolCalls[0].Arguments = `{"query":`
			case "duplicate":
				message.ToolCalls[0].Arguments = `{"query":"x","query":"y"}`
			case "array":
				message.ToolCalls = append(message.ToolCalls, llm.ToolCall{ID: "other", Name: "plan", Arguments: `{}`})
			}
			_, _, _ = r.nativeStep(context.Background(), message, Direct, func(kind, _ string) error {
				if mode == "recording" && kind == "call_resolution" {
					return errors.New("recording failed")
				}
				return nil
			}, nil, func(string, bool, string) string { return "" })
			if calls != 0 {
				t.Fatalf("%s executed %d calls", mode, calls)
			}
		})
	}
}
