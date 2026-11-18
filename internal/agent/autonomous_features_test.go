package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestAutonomousFeatureGuidanceOnlyListsAdvertisedTools(t *testing.T) {
	reg := tool.NewRegistry(prompts.Builtin())
	for _, name := range []string{"plan", "elaborate", "wait", "check", "read", "write"} {
		reg.Register(tool.Definition{Name: name})
	}
	r := &Runner{Prompts: prompts.Builtin(), Registry: reg}
	got := r.autonomousFeatureGuidance()
	for _, want := range []string{"plan({})", "elaborate", "still_waiting", "check tool"} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance missing advertised feature %q: %s", want, got)
		}
	}
	if strings.Contains(got, "library_workflow") {
		t.Fatalf("guidance named a tool outside the current registry: %s", got)
	}
}

func TestExplicitPlanningGateRetainsElaborationWithoutExecutionTools(t *testing.T) {
	reg := tool.NewRegistry(prompts.Builtin())
	for _, name := range []string{"plan", "elaborate", "wait", "build_block", "build_workflow", "library_workflow", "read", "write", "bash"} {
		reg.Register(tool.Definition{Name: name})
	}
	r := &Runner{
		Prompts:      prompts.Builtin(),
		Registry:     reg,
		Assigned:     &AssignedOps{ProjectCoding: func(context.Context) error { return errTestPlanningGate }},
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "store", Kind: "root"}},
	}
	view := r.ProjectPhaseRegistry(reg)
	for _, name := range []string{"plan", "elaborate", "wait", "build_block", "build_workflow", "library_workflow", "read"} {
		if _, ok := view.Lookup(name); !ok {
			t.Errorf("explicit planning view lost %s", name)
		}
	}
	for _, name := range []string{"write", "bash"} {
		if _, ok := view.Lookup(name); ok {
			t.Errorf("explicit planning view gained execution tool %s", name)
		}
	}

	direct := &Runner{Prompts: prompts.Builtin(), Registry: reg, Assigned: r.Assigned}
	if got := direct.ProjectPhaseRegistry(reg); got != reg {
		t.Fatal("unselected direct coding view was narrowed")
	}
}

// Per-request dynamic state (workflow scope header, tool-surface delta,
// run budget) must not be rewritten into the retained system message: any
// change there invalidates the provider's cached prefix for the whole
// transcript. It rides on the request tail instead.
func TestDynamicPromptTailKeepsSystemMessageStable(t *testing.T) {
	reg := tool.NewRegistry(prompts.Builtin())
	reg.Register(tool.Definition{Name: "plan"})
	r := &Runner{Prompts: prompts.Builtin(), Registry: reg}
	r.workflowHeader = "SCOPE state=one"
	r.toolSurfaceNote = "surface one"
	first := r.basePrompt()
	r.workflowHeader = "SCOPE state=two"
	r.toolSurfaceNote = "surface two"
	if second := r.basePrompt(); second != first {
		t.Fatalf("system message changed with dynamic state:\n%q\n%q", first, second)
	}
	tail := r.dynamicPromptTail()
	if !strings.Contains(tail, "SCOPE state=two") || !strings.Contains(tail, "surface two") {
		t.Fatalf("dynamic tail missing current state: %q", tail)
	}
	if strings.Contains(first, "SCOPE") || strings.Contains(first, "surface") {
		t.Fatalf("dynamic state leaked into system message: %q", first)
	}
}

// The model must see a truthful measured remaining-context figure in
// the run budget: window minus the larger of the retained estimate and
// the last server-measured prompt. Without it a model can believe it is
// out of context while tens of thousands of tokens remain.
func TestDynamicPromptTailReportsRemainingContext(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(prompts.Builtin()), MaxTokens: 256000}
	r.lastMeasuredPrompt = 235900
	tail := r.dynamicPromptTail()
	if !strings.Contains(tail, `"ContextWindow":256000`) || !strings.Contains(tail, `"RemainingContextTokens":20100`) {
		t.Fatalf("tail missing measured remaining context: %q", tail)
	}
}

var errTestPlanningGate = testPlanningGateError("planning")

type testPlanningGateError string

func (e testPlanningGateError) Error() string { return string(e) }
