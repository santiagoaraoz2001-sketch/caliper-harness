package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func methodLensFixture() *session.MethodLens {
	return &session.MethodLens{
		WorkflowID: "inspect-flow", WorkflowVersion: 1, Title: "Inspect flow", Purpose: "Inspect before changing",
		Steps:     []tool.MethodStep{{BlockID: "inspect-thing", BlockVersion: 1, Title: "Inspect thing"}},
		StepIndex: 0, BlockID: "inspect-thing", BlockVersion: 1, PhaseTitle: "Inspect thing",
		Instructions: "Read the target exactly.", CompletionEvidence: "observed behavior", FailureHandling: "stay here",
	}
}

func TestMethodLensSettlesBindsScopeAndEntersDynamicTail(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), Session: &recordSink{}, SessionID: "lens-session"}
	r.Continuation.Version = 1
	r.Continuation.Add("intent", "user", "inspect the target", false)
	r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "approach", ID: "approach-a"}
	def := tool.Definition{Name: "library_workflow", Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: `{"method_lens":"candidate"}`, MethodLens: &tool.MethodLensUpdate{Mode: "attach", Lens: methodLensFixture()}}, nil
	}}
	if _, _, ok := r.invokeValidated(t.Context(), def, tool.Args{}, Direct); !ok {
		t.Fatal("attach invocation failed")
	}
	if r.Continuation.MethodLens != nil {
		t.Fatal("method lens changed continuation before durable settlement")
	}
	if err := r.settleFocus(func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	lens := r.Continuation.MethodLens
	if lens == nil || lens.ScopeKind != "approach" || lens.ScopeID != "approach-a" || lens.Revision != 1 {
		t.Fatalf("settled lens not bound to current scope: %+v", lens)
	}
	tail := r.dynamicPromptTail()
	for _, want := range []string{"inspect-flow", "Read the target exactly.", "PAT acceptance", "unverified"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("dynamic tail missing %q: %s", want, tail)
		}
	}
	r.Continuation.Workflow.ID = "approach-b"
	if tail := r.dynamicPromptTail(); strings.Contains(tail, "Read the target exactly.") {
		t.Fatalf("method lens leaked into another approach scope: %s", tail)
	}
}

func TestMethodLensPhaseRequiresSameWorkflowAndReviewerCannotAttach(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), Session: &recordSink{}, SessionID: "lens-session"}
	r.Continuation.Version = 1
	r.Continuation.Add("intent", "user", "inspect the target", false)
	r.Continuation.MethodLens = methodLensFixture()
	r.Continuation.MethodLens.ScopeKind = "session"
	wrong := methodLensFixture()
	wrong.WorkflowID = "other-flow"
	def := tool.Definition{Name: "library_workflow", Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: `{"method_lens":"candidate"}`, MethodLens: &tool.MethodLensUpdate{Mode: "phase", Lens: wrong}}, nil
	}}
	text, _, ok := r.invokeValidated(t.Context(), def, tool.Args{}, Direct)
	if !ok || !strings.Contains(text, "refused") {
		t.Fatalf("mismatched phase was not refused: ok=%t text=%s", ok, text)
	}
	if err := r.settleFocus(func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.MethodLens.WorkflowID != "inspect-flow" {
		t.Fatalf("mismatched phase replaced lens: %+v", r.Continuation.MethodLens)
	}

	r.Reviewing = true
	attach := tool.Definition{Name: "library_workflow", Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{Text: `{"method_lens":"candidate"}`, MethodLens: &tool.MethodLensUpdate{Mode: "attach", Lens: methodLensFixture()}}, nil
	}}
	r.Continuation.MethodLens = nil
	text, _, ok = r.invokeValidated(t.Context(), attach, tool.Args{}, Direct)
	if !ok || !strings.Contains(text, "refused") {
		t.Fatalf("reviewer attach was not refused: ok=%t text=%s", ok, text)
	}
	if err := r.settleFocus(func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.MethodLens != nil {
		t.Fatalf("reviewer gained a method lens: %+v", r.Continuation.MethodLens)
	}
}
