package run

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/graph"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Controlled receipts isolate binding edge cases; public wire tests use real Kuzu/Ready/claim.
func TestPreparedTaskBindingReceiptAndSnapshot(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "partial after valid", "omitted data", "blocked", "truncated", "failed", "multiple", "scope", "focus", "reset", "explicit selection", "blank"} {
		t.Run(mode, func(t *testing.T) {
			r := &agent.Runner{Workspace: "/project", SessionID: "run", Assigned: &agent.AssignedOps{Store: "store", Reset: func() {}}}
			calls := 0
			result := func(id string) tool.Result {
				v := graph.Result{Data: json.RawMessage(`{}`)}
				for i, step := range []string{"createPlan", "createApproach", "createTask", "setTaskBlock"} {
					node := fmt.Sprint(i)
					if i >= 2 {
						node = id
					}
					v.Effects = append(v.Effects, graph.Effect{Key: "prepareTask", Field: "prepareTask", Step: step, Status: "applied", ID: node})
				}
				if mode == "partial" || mode == "partial after valid" && id == "other" {
					v.Effects[3].Status = "failed"
				}
				if mode == "omitted data" {
					v.DataOmitted = true
				}
				raw, _ := json.Marshal(v)
				return tool.Result{Text: string(raw), Blocked: mode == "blocked", Truncated: mode == "truncated", Failed: mode == "failed"}
			}
			prepare := tool.Definition{Name: "prepare_task", Bind: func(_ context.Context, a tool.Args) (tool.Definition, tool.Args, error) {
				return tool.Definition{Name: "graphql", Execute: func(context.Context, tool.Args) (tool.Result, error) { return result(a["id"].(string)), nil }}, a, nil
			}}
			typed := func(name string) tool.Definition {
				return tool.Definition{Name: name, Params: []tool.Param{{Name: "task", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
					calls++
					return tool.Result{Text: a["task"].(string)}, nil
				}}
			}
			p, ready, start := bindPreparedTask(r, prepare, typed("ready_task"), typed("task_start"))
			makeTask := func(id string) {
				d, a, e := p.Bind(t.Context(), tool.Args{"id": id})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = d.Execute(t.Context(), a); e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e := ready.Bind(t.Context(), tool.Args{}); e == nil {
				t.Fatal("omission before creation admitted")
			}
			makeTask("actual")
			bound, args, err := ready.Bind(t.Context(), tool.Args{})
			if mode == "partial" || mode == "omitted data" || mode == "blocked" || mode == "truncated" || mode == "failed" {
				if err == nil {
					t.Fatal("incomplete receipt bound")
				}
				return
			}
			if err != nil || args["task"] != "actual" || args["_prepared_task"] != "actual" || len(args["_preparation_receipt_sha256"].(string)) != 64 {
				t.Fatalf("binding: %v %v", args, err)
			}
			switch mode {
			case "multiple", "partial after valid":
				makeTask("other")
			case "scope":
				r.Workspace = "/different"
			case "focus":
				r.Assigned.Observe(tool.Definition{}, nil, tool.Result{Focus: &tool.WorkflowScope{Kind: "task", ID: "other"}}, nil)
			case "reset":
				r.Assigned.Reset()
			case "explicit selection":
				d, a, e := ready.Bind(t.Context(), tool.Args{"task": "other"})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = d.Execute(t.Context(), a); e != nil {
					t.Fatal(e)
				}
			case "blank":
				if _, _, e := ready.Bind(t.Context(), tool.Args{"task": ""}); e == nil {
					t.Fatal("explicit empty treated as omission")
				}
			}
			_, err = bound.Execute(t.Context(), args)
			stale := mode == "multiple" || mode == "partial after valid" || mode == "scope" || mode == "focus" || mode == "reset" || mode == "explicit selection"
			if (err != nil) != stale {
				t.Fatalf("stale=%v err=%v", stale, err)
			}
			if stale {
				return
			}
			d, a, e := start.Bind(t.Context(), tool.Args{})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = d.Execute(t.Context(), a); e != nil {
				t.Fatal(e)
			}
			if _, _, e = start.Bind(t.Context(), tool.Args{}); e == nil {
				t.Fatal("consumed receipt remained bindable")
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestPreparedTaskBindingDoesNotRestoreHistoricalSelection(t *testing.T) {
	r := &agent.Runner{Assigned: &agent.AssignedOps{Store: "store", Reset: func() {}}, Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "store", Kind: "task", ID: "historical"}}}
	noop := tool.Definition{Name: "prepare_task", Bind: func(context.Context, tool.Args) (tool.Definition, tool.Args, error) { panic("no creation") }}
	_, ready, _ := bindPreparedTask(r, noop, tool.Definition{Name: "ready_task", Params: []tool.Param{{Name: "task", Type: tool.TypeString, Required: true}}}, tool.Definition{Name: "task_start"})
	if _, _, e := ready.Bind(t.Context(), tool.Args{}); e == nil {
		t.Fatal("historical focus fabricated a fresh preparation receipt")
	}
}

type bindingArrayModel struct {
	first llm.Reply
	calls int
}

func (*bindingArrayModel) ToolProtocol() string { return "structured" }
func (*bindingArrayModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	panic("native only")
}
func (m *bindingArrayModel) ChatTools(context.Context, []llm.Message, []llm.ToolDefinition, func(string)) (llm.Reply, error) {
	m.calls++
	if m.calls == 1 {
		return m.first, nil
	}
	return llm.Reply{Message: llm.Message{Role: "assistant", Thinking: "no final"}, FinishReason: "stop"}, nil
}
func TestPreparedTaskArrayCannotBindFutureCreation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Coding.CheckCommand = "true"
	cfg.Agent.LoopRepeat = 0
	cfg.Agent.LoopStall = 0
	bindDecisionFixture(t, &cfg, func(decision.Request) int { return 0 })
	r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Model: "component", Window: 32768, MaxSteps: 5, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "array"}, "prepare once")
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]string{"plan_title": "plan", "plan_goal": "goal", "approach_title": "approach", "task_title": "task", "task_prompt": "repair", "current": "observed", "target": "correct", "sketch": "fix owner", "acceptance": "regression", "tests": "owning tests", "verify": "inspect", "acceptance_command": "true"}
	raw, _ := json.Marshal(input)
	m := &bindingArrayModel{first: llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "prepare", Name: "prepare_task", Arguments: string(raw)}, {ID: "ready", Name: "ready_task", Arguments: `{}`}}}, FinishReason: "tool_calls"}}
	r.Model = m
	if _, _, e = r.Run(t.Context(), "prepare once"); e != nil {
		t.Fatal(e)
	}
	plans, e := plan.ListPlans(s, "")
	if e != nil || len(plans) != 0 || r.Continuation.Assignment != nil {
		t.Fatalf("forward binding created effects: %v %+v %v", plans, r.Continuation.Assignment, e)
	}
	refused := 0
	for _, entry := range r.Continuation.Entries {
		if entry.Message.Role == "tool" && strings.Contains(entry.Message.Content, "entire call array refused; no calls ran") {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("lost correlated preflight refusals: %d", refused)
	}
}
