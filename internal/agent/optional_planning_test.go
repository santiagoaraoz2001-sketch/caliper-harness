package agent

import (
	"context"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestOptionalPlanningArrayCannotBorrowFuturePhase(t *testing.T) {
	reg := tool.NewRegistry(prompts.Builtin())
	effects := 0
	for _, name := range []string{"plan", "write"} {
		reg.Register(tool.Definition{Name: name, Execute: func(context.Context, tool.Args) (tool.Result, error) {
			effects++
			return tool.Result{Text: "effect"}, nil
		}})
	}
	r := &Runner{Prompts: prompts.Builtin(), Registry: reg, Continuation: session.Continuation{Version: 1}}
	r.Continuation.Add("intent", "user", "enter plan then write", false)
	reply := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "enter", Name: "plan", Arguments: `{}`}, {ID: "effect", Name: "write", Arguments: `{}`}}}
	remaining := 2
	_, _, err := r.nativeStep(context.Background(), reply, Direct, func(string, string) error { return nil }, &remaining, func(string, bool, string) string { return "" })
	if err != nil || effects != 0 {
		t.Fatalf("phase-changing array executed%d err%v", effects, err)
	}
	if err = r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestOptionalPlanningHistoricalAssignmentNotActive(t *testing.T) {
	r := &Runner{Continuation: session.Continuation{Assignment: &session.Assignment{Store: "old", TaskID: "old", Attempt: 1}}}
	if r.PlanningActive() {
		t.Fatal("history engaged plan")
	}
	r.Continuation.Workflow = &session.WorkflowScope{Store: "actual", Kind: "root"}
	if !r.PlanningActive() {
		t.Fatal("explicit root did not engage planning")
	}
}

func TestOptionalPlanningFinalRequiresCurrentSelectedPlan(t *testing.T) {
	for _, mode := range []string{"complete", "unfinished", "other-plan", "historical"} {
		t.Run(mode, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Register(tool.Definition{Name: "workflow", WorkflowRefArg: "plan", ResolveWorkflowRef: func(_ context.Context, a tool.Args) (string, error) { return a["plan"].(string), nil }, Params: []tool.Param{{Name: "plan", Type: tool.TypeString, Required: true}}})
			replies := []string{"```tool\ntool: workflow\nplan: current\n```", "done"}
			if mode == "historical" {
				replies = []string{"done"}
			}
			r := &Runner{Model: &scriptModel{replies: replies}, Registry: reg, MaxSteps: 2, Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "actual", Kind: "plan", ID: "current"}}, Exec: func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				return tool.Result{Workflow: &outcome.Report{Ref: "current", Qualified: true, Outcome: outcome.Outcome{Status: outcome.Completed}}}, nil
			}}
			r.PlanningCompletion = func(*session.WorkflowScope) (*outcome.Report, error) {
				ref := "current"
				if mode == "other-plan" {
					ref = "other"
				}
				return &outcome.Report{Ref: ref, Qualified: mode != "unfinished"}, nil
			}
			_, _, err := r.Run(context.Background(), "complete selected work")
			if err != nil {
				t.Fatal(err)
			}
			if (r.Outcome.Status == outcome.Completed) != (mode == "complete") {
				t.Fatalf("%s: %+v", mode, r.Outcome)
			}
		})
	}
}

func TestOptionalPlanningDispatchUsesChildOwnerNotParentClaim(t *testing.T) {
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "actual", Kind: "approach", ID: "a"}}, Assigned: &AssignedOps{Admit: func(context.Context) error { t.Fatal("parent claim demanded for owned child dispatch"); return nil }}}
	invoked := false
	_, e := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		invoked = true
		return tool.Result{}, nil
	})(context.Background(), tool.Definition{Name: "launch"}, tool.Args{})
	if e != nil || !invoked {
		t.Fatalf("dispatch missing: %v", e)
	}
}
