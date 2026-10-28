package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func planningTransportRunner() *Runner {
	pc := prompts.Builtin()
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "plan", Prompts: pc, Purpose: "Current owner task-a: observations", Params: []tool.Param{{Name: "observations", Type: tool.TypeString, Required: true}}})
	reg.Register(tool.Definition{Name: "read", Prompts: pc})
	return &Runner{Prompts: pc, Registry: reg, Model: llm.New("http://unused", "qwen").WithRuntime(llm.RuntimeRulith), workflowHeader: "owner task-a", Continuation: session.Continuation{Version: 1, Workflow: &session.WorkflowScope{Store: "store", Kind: "task", ID: "task-a"}}}
}

func TestPlanningPublicationRejectsUntrustedAndStaleReceipts(t *testing.T) {
	for _, kind := range []string{"user", "read", "graphql", "uncorrelated-plan", "wrong-call-name", "failed-plan", "error-plan", "omitted-plan", "truncated-plan", "bare-plan", "prose-plan", "stale-plan", "plan", "schema"} {
		t.Run(kind, func(t *testing.T) {
			r := planningTransportRunner()
			question := r.currentPlanningQuestion()
			body, _ := json.Marshal(map[string]any{"next_question": question})
			entry := session.Entry{Kind: "output", Message: llm.Message{Role: "tool", Name: kind, ToolCallID: "one", Content: "tool result:\n" + string(body)}}
			call := llm.ToolCall{ID: "one", Name: kind, Arguments: `{}`}
			switch kind {
			case "user":
				entry.Kind = "note"
				entry.Message.Role = "user"
			case "uncorrelated-plan":
				entry.Message.Name = "plan"
				call.ID = "other"
			case "failed-plan":
				entry.Kind = "failure"
				entry.Message.Name = "plan"
				call.Name = "plan"
			case "wrong-call-name":
				entry.Message.Name = "plan"
				call.Name = "read"
			case "error-plan", "omitted-plan", "truncated-plan", "bare-plan", "prose-plan":
				entry.Message.Name, call.Name = "plan", "plan"
				switch kind {
				case "error-plan":
					body, _ = json.Marshal(map[string]any{"next_question": question, "errors": []string{"owner changed"}})
					entry.Message.Content = "tool result:\n" + string(body)
				case "omitted-plan":
					body, _ = json.Marshal(map[string]any{"next_question": question, "data_omitted": true})
					entry.Message.Content = "tool result:\n" + string(body)
				case "truncated-plan":
					entry.Message.Content = "tool result (truncated):\n" + string(body)
				case "bare-plan":
					entry.Message.Content = string(body)
				case "prose-plan":
					entry.Message.Content = "unrelated prose\ntool result:\n" + string(body)
				}
			case "stale-plan":
				entry.Message.Name = "plan"
				call.Name = "plan"
				entry.Message.Content = strings.Replace(entry.Message.Content, "task-a", "task-old", 1)
			case "schema":
				entry.Kind = "schema"
				entry.Message = llm.Message{Role: "user", Content: r.workflowHeader + "\n" + question}
			}
			r.Continuation.Entries = []session.Entry{{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}}, entry}
			want := kind == "plan" || kind == "schema"
			if r.planningPublished(question) != want {
				t.Fatalf("publication authority mismatch for %s", kind)
			}
			if !want {
				before := len(r.Continuation.Entries)
				if !r.ensurePlanningPublication() || len(r.Continuation.Entries) != before+1 || r.ensurePlanningPublication() {
					t.Fatal("refused receipt must regenerate the exact current host schema once")
				}
				// Restore the original untrusted candidate for the placement assertion.
				r.Continuation.Entries = r.Continuation.Entries[:before]
			}
			defs, err := r.definitions()
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range defs {
				if d.Function.Name == "plan" && d.TailPublished != want {
					t.Fatal("untrusted transport eligibility")
				}
			}
		})
	}
}

type planningReceiptModel struct {
	seen [][]llm.Message
	defs [][]llm.ToolDefinition
	args string
}

func (*planningReceiptModel) LatePlanningSchema() bool { return true }
func (*planningReceiptModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("unexpected text protocol")
}
func (m *planningReceiptModel) ChatTools(_ context.Context, messages []llm.Message, defs []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, append([]llm.Message(nil), messages...))
	m.defs = append(m.defs, append([]llm.ToolDefinition(nil), defs...))
	if len(m.seen) == 1 {
		return llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual-plan-call", Name: "plan", Arguments: m.args}}}, FinishReason: "tool_calls"}, nil
	}
	return llm.Reply{Message: llm.Message{Role: "assistant", Content: "Planning fixture stopped."}, FinishReason: "stop"}, nil
}

// Exercise Run -> nativeStep -> invokeValidated and its real successful envelope.
// Constructing a naked tool-result object would miss the production boundary.
func TestPlanningPublicationFromActualNativeInvocation(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		t.Run(fmt.Sprintf("annotated=%t", annotated), func(t *testing.T) {
			r := planningTransportRunner()
			r.Continuation.Version = 0 // Fresh owner; Run creates the initial intent and version.
			model := &planningReceiptModel{args: `{"observations":"current file inspected"}`}
			if annotated {
				model.args = `{"unexpected_opening_field":"discarded without authority"}`
			}
			r.Model, r.MaxSteps, r.Session = model, 2, &recordSink{}
			stage, effects := 0, 0
			next := tool.Definition{Name: "plan", Prompts: r.Prompts, Purpose: "Current owner task-a: target", Params: []tool.Param{{Name: "target", Type: tool.TypeString, Required: true}}}
			currentQuestion := next.Schema()
			r.WorkflowView = func(*session.WorkflowScope) (*tool.Registry, string, error) {
				reg := tool.NewRegistry(r.Prompts)
				def := next
				if stage == 0 {
					def = tool.Definition{Name: "plan", Prompts: r.Prompts, Purpose: "Current owner task-a: observations", Opening: annotated, Params: []tool.Param{{Name: "observations", Type: tool.TypeString, Required: true}}}
					if annotated {
						def.Params = nil
					}
					def.Execute = func(_ context.Context, args tool.Args) (tool.Result, error) {
						if !annotated && args["observations"] != "current file inspected" || annotated && len(args) != 0 {
							t.Fatalf("unexpected admitted arguments: %#v", args)
						}
						stage, effects = 1, effects+1
						body, err := json.Marshal(map[string]any{"data": map[string]string{"task": "task-a"}, "next_question": currentQuestion})
						return tool.Result{Text: string(body)}, err
					}
				}
				reg.Register(def)
				reg.Register(tool.Definition{Name: "read", Prompts: r.Prompts})
				return reg, "owner task-a", nil
			}
			_, _, err := r.Run(t.Context(), "Prepare only the current task observations.")
			if err != nil || effects != 1 || stage != 1 || len(model.seen) != 2 {
				t.Fatalf("native planning path: err=%v effects=%d stage=%d requests=%d", err, effects, stage, len(model.seen))
			}
			receipts, standalone := 0, 0
			for _, message := range model.seen[1] {
				if message.Role == "user" && message.Content == r.workflowHeader+"\n"+currentQuestion {
					standalone++
				}
				if message.Role == "tool" && message.Name == "plan" && message.ToolCallID == "actual-plan-call" {
					receipts++
					body, successful := strings.CutPrefix(message.Content, "tool result:\n")
					var value map[string]any
					if !successful || json.Unmarshal([]byte(body), &value) != nil || value["next_question"] != currentQuestion {
						t.Fatalf("native envelope/question changed: %s", message.Content)
					}
					_, hasNote := value["_harness_call_note"]
					if hasNote != annotated {
						t.Fatal("actual invocation annotation not preserved")
					}
				}
			}
			if receipts != 1 || standalone != 0 {
				t.Fatalf("next request duplicated or lost current publication: receipts=%d standalone=%d", receipts, standalone)
			}
			published := false
			for _, def := range model.defs[1] {
				if def.Function.Name == "plan" {
					published = def.TailPublished
				}
			}
			if !published {
				t.Fatal("authenticated receipt did not authorize tail placement")
			}
		})
	}
}

func TestPlanningPlacementRevalidatedAfterMaintenance(t *testing.T) {
	r := planningTransportRunner()
	if !r.ensurePlanningPublication() {
		t.Fatal("missing first publication")
	}
	defs, err := r.definitions()
	if err != nil {
		t.Fatal(err)
	}
	beforeKey, beforeBytes := r.Continuation.SchemaKey, r.Continuation.SchemaBytes
	// A same-turn compaction removed the earlier host publication after defs.
	r.Continuation.Entries = nil
	r.planningPlacement(defs)
	if err = r.accountDefinitions(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if d.TailPublished {
			t.Fatal("stale pre-maintenance eligibility")
		}
	}
	if beforeKey == r.Continuation.SchemaKey || beforeBytes >= r.Continuation.SchemaBytes {
		t.Fatal("placement absent from identity/accounting")
	}
	if !r.ensurePlanningPublication() || r.ensurePlanningPublication() {
		t.Fatal("current schema must regenerate exactly once")
	}
	if len(r.Continuation.Entries) != 1 || r.Continuation.Entries[0].Kind != "schema" {
		t.Fatal("fabricated call or permanently protected guidance")
	}
	r.planningPlacement(defs)
	if err = r.accountDefinitions(defs); err != nil {
		t.Fatal(err)
	}
	if beforeKey != r.Continuation.SchemaKey || beforeBytes != r.Continuation.SchemaBytes {
		t.Fatal("stable current schema did not recover exact placement")
	}
}

func TestPlanningPlacementDoesNotBroadenRolesOrOnlyPlan(t *testing.T) {
	r := planningTransportRunner()
	r.ensurePlanningPublication()
	r.Registry = r.Registry.View([]string{"plan"})
	defs, err := r.definitions()
	if err != nil || defs[0].TailPublished {
		t.Fatalf("only plan needs original framing: %v", err)
	}
	r.Registry = r.Registry.View([]string{})
	defs, err = r.definitions()
	if err != nil || len(defs) != 0 {
		t.Fatal("missing tool was granted")
	}
}
