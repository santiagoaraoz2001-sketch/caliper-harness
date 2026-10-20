package run

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestFactoryBindsLegacyWorkflowToCapturedProject(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	foreign, err := plan.CreatePlanContext(plan.WithProject(t.Context(), t.TempDir()), s, "foreign", "private")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Behavior.Path, cfg.Decision.Disabled = "", true
	d := Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://unused", Model: "not-requested", In: strings.NewReader(""), Out: io.Discard}
	r, _, err := New(d, "inspect this project")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if r.Workspace != root {
		t.Fatalf("factory lost captured workspace: %q", r.Workspace)
	}
	build, _ := r.Registry.Lookup("workflow_approach")
	if _, err = build.Execute(t.Context(), tool.Args{"plan": foreign.ID, "title": "unexpected"}); err == nil || !strings.Contains(err.Error(), "outside captured project") {
		t.Fatalf("registered build did not retain scope: %v", err)
	}
	focus, _ := r.Registry.Lookup("workflow_focus")
	if _, _, err = focus.Bind(t.Context(), tool.Args{"ref": foreign.ID, "limit": int64(20)}); err == nil || !strings.Contains(err.Error(), "outside captured project") {
		t.Fatalf("registered focus did not retain scope: %v", err)
	}
	// Restoring a foreign scope is refused by the existing managed-planning
	// owner before a model request or a foreign brief can be produced.
	if _, _, err = r.WorkflowView(&session.WorkflowScope{Store: s.Identity(), Kind: "plan", ID: foreign.ID}); err == nil || !strings.Contains(err.Error(), "outside captured project") {
		t.Fatalf("foreign continuation accepted: %v", err)
	}
}
