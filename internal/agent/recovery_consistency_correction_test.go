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

// The model already receives the correlated receipt. Recovery may identify the
// next legal action, but must not turn that same receipt into a compulsory query.
func TestRecoveryConsistencyNativeReceipts(t *testing.T) {
	for _, mode := range []string{"query", "committed", "partial", "non-graphql-partial"} {
		t.Run(mode, func(t *testing.T) {
			name := "graphql"
			text := `{"data":{"plan":{"id":"actual-plan","state":"draft"}},"effects":[]}`
			var failure error
			if mode == "committed" {
				text = `{"data":{"createTask":{"id":"actual-task"}},"effects":[{"field":"createTask","status":"applied","id":"actual-task"}]}`
			}
			if mode == "partial" {
				text = `{"effects":[{"field":"createTask","status":"applied","id":"actual-task"},{"field":"transitionApproach","status":"unknown"}],"errors":["readiness refused"]}`
				failure = errors.New("readiness refused")
			}
			if mode == "non-graphql-partial" {
				name, text, failure = "prepare_task", "committed Plan actual-plan; Approach actual-approach; task write failed", errors.New("task write failed")
			}
			pc := prompts.Builtin()
			reg := tool.NewRegistry(pc)
			reg.Register(tool.Definition{Name: name, Params: []tool.Param{{Name: "query", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
				return tool.Result{Text: text, Failed: failure != nil}, failure
			}})
			r := &Runner{Prompts: pc, Registry: reg, Assigned: &AssignedOps{Store: "/actual-store"}, Workspace: "/actual-workspace", Session: &recordSink{}, SessionID: "actual-session", Continuation: session.Continuation{Version: 1, Workflow: &session.WorkflowScope{Store: "/actual-store", Kind: "root"}}}
			r.Assigned.Facts = func(context.Context) (any, error) {
				return BookkeepingFacts{Preparation: "Answer the current approach strategy question."}, nil
			}
			r.Continuation.Add("intent", "user", "preserve actual receipts and continue the current question", false)
			reply := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual-call", Name: name, Arguments: `{"query":"{version}"}`}}}
			remaining := 1
			_, _, err := r.nativeStep(t.Context(), reply, r.assignmentExec(Direct), func(string, string) error { return nil }, &remaining, func(string, bool, string) string { return "" })
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Continuation.Entries) != 3 || r.Continuation.Entries[2].Message.ToolCallID != "actual-call" || !strings.Contains(r.Continuation.Entries[2].Message.Content, text) {
				t.Fatalf("actual correlated receipt changed: %+v", r.Continuation.Entries)
			}
			if ok, err := r.publishPreparationGuidance(t.Context(), "Continue answering the current Q/A question.", true, ""); err != nil || !ok {
				t.Fatalf("recovery: published%t err%v", ok, err)
			}
			guidance := r.Continuation.Entries[len(r.Continuation.Entries)-1].Message.Content
			if strings.Contains(strings.ToLower(guidance), "readback") || strings.Contains(strings.ToLower(guidance), "read back") {
				t.Fatalf("recovery imposed another query on a retained receipt: %s", guidance)
			}
			if !strings.Contains(guidance, "Answer the current approach strategy question.") {
				t.Fatal("current owning action missing", guidance)
			}
			if failure != nil && (!strings.Contains(guidance, failure.Error()) || !strings.Contains(guidance, "actual-")) {
				t.Fatal("failure/committed identity provenance lost", guidance)
			}
			if err := r.Continuation.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoveryConsistencyTextReceipt(t *testing.T) {
	pc := prompts.Builtin()
	reg := tool.NewRegistry(pc)
	reg.Register(tool.Definition{Name: "graphql", Params: []tool.Param{{Name: "query", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: `{"data":{"plan":{"id":"actual-plan","state":"draft"}},"effects":[]}`}, nil
	}})
	r := &Runner{Prompts: pc, Model: &scriptModel{replies: []string{"```tool\ntool: graphql\nquery: {version}\n```"}}, Registry: reg, Assigned: &AssignedOps{Store: "/store", Reset: func() {}}, Session: &recordSink{}, MaxSteps: 1, Workspace: "/workspace", Continuation: session.Continuation{Version: 1, Workflow: &session.WorkflowScope{Store: "/store", Kind: "root"}}}
	r.Continuation.Add("intent", "user", "earlier request", false)
	if _, _, err := r.Run(t.Context(), "inspect current plan"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(r.preparationDisposition), "readback") || strings.Contains(strings.ToLower(r.preparationDisposition), "read back") {
		t.Fatal("text route imposed another query", r.preparationDisposition)
	}
	if !strings.Contains(r.preparationDisposition, "effects/errors/omission") {
		t.Fatal("GraphQL receipt provenance missing", r.preparationDisposition)
	}
}

func TestRecoveryConsistencyTextBlockedKeepsResult(t *testing.T) {
	pc := prompts.Builtin()
	reg := tool.NewRegistry(pc)
	reg.Register(tool.Definition{Name: "prepare_task", Params: []tool.Param{{Name: "query", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: "Plan actual-plan committed; task creation blocked", Blocked: true}, nil
	}})
	r := &Runner{Prompts: pc, Model: &scriptModel{replies: []string{"```tool\ntool: prepare_task\nquery: actual\n```"}}, Registry: reg, Assigned: &AssignedOps{Store: "/store", Reset: func() {}}, Session: &recordSink{}, MaxSteps: 1, Workspace: "/workspace", Continuation: session.Continuation{Version: 1, Workflow: &session.WorkflowScope{Store: "/store", Kind: "root"}}}
	r.Continuation.Add("intent", "user", "earlier request", false)
	if _, _, err := r.Run(t.Context(), "prepare intended work"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.preparationDisposition, "no calls ran") || !strings.Contains(r.preparationDisposition, "Plan actual-plan committed") {
		t.Fatal("blocked preparation misreported actual effects", r.preparationDisposition)
	}
}
