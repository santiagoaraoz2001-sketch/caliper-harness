package agent

import (
	"testing"

	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestPlanningPhaseRetainsLibraryAndWaitTools(t *testing.T) {
	reg := tool.NewRegistry()
	for _, name := range []string{"read", "bash", "edit", "wait", "build_block", "build_workflow", "library_workflow", "plan", "ask", "graphql"} {
		reg.Register(tool.Definition{Name: name})
	}
	r := &Runner{
		Assigned:     &AssignedOps{},
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "s", Kind: "approach", ID: "a1"}},
	}
	view := r.ProjectPhaseRegistry(reg)
	for _, name := range []string{"wait", "build_block", "build_workflow", "library_workflow"} {
		if _, ok := view.Lookup(name); !ok {
			t.Errorf("planning phase dropped %s; names=%v", name, view.Names())
		}
	}
	for _, name := range []string{"bash", "edit"} {
		if _, ok := view.Lookup(name); ok {
			t.Errorf("planning phase retained execution tool %s", name)
		}
	}
}
