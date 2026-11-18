package run

import (
	"testing"

	"specific-harness/internal/launch"
	"specific-harness/internal/session"
)

// User-scoped library authoring and read-only waiting are metadata work:
// they must stay available while a PAT scope is being planned, without
// granting project execution tools before a claim.
func TestPlanningViewRetainsLibraryAndWaitTools(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	n, err := launch.ScopeNode(f.d.Store, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	scope := &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: n.Parent}
	for _, exposure := range []string{"", "coding", "full", "workflow"} {
		t.Run(exposure, func(t *testing.T) {
			r := consistencyRunner(t, f, scope, exposure)
			view, _, err := r.WorkflowView(scope)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"wait", "build_block", "build_workflow", "library_workflow"} {
				if _, ok := view.Lookup(name); !ok {
					t.Errorf("planning view (%q) lacks %s; names=%v", exposure, name, view.Names())
				}
			}
			for _, name := range []string{"bash", "edit", "write"} {
				if _, ok := view.Lookup(name); ok {
					t.Errorf("unclaimed planning view (%q) gained execution tool %s", exposure, name)
				}
			}
		})
	}
}
