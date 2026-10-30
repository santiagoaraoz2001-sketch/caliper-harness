package run

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestPlanningRootReturnPreservesDraftAndExplicitSelection(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	f.call(tool.Args{"current": "observed", "target": "working"})
	f.call(tool.Args{"acceptance": "assert behavior", "tests": "unexecuted", "acceptance_command": "true"})
	prior := *f.scope
	before, _ := plan.GetTask(f.d.Store, prior.ID)
	question := f.definition().Schema()
	cfg := config.Defaults()
	cfg.Templates.Path = assignedTemplates()
	cfg.Recall.TopN = 0
	t.Chdir(f.d.Workspace)
	runner, _, err := New(Deps{Tools: "coding", Cfg: cfg, Store: f.d.Store, Server: "http://unused.invalid", Model: "component", Window: 32768, Continuation: session.Continuation{Workflow: f.scope}}, "continue the saved task")
	if err != nil {
		t.Fatal(err)
	}
	focus, ok := runner.Registry.Lookup("workflow_focus")
	if !ok {
		t.Fatal("projected registry lost focus")
	}
	bound, args, err := focus.Bind(t.Context(), tool.Args{"ref": "root"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := bound.Execute(t.Context(), args)
	if err != nil || res.Focus.Kind != "root" || !strings.Contains(res.Text, `workflow_focus(ref="`+prior.ID+`")`) {
		t.Fatalf("root authority or return instruction lost: %+v %v", res, err)
	}
	f.scope = res.Focus
	if _, repair := tool.ValidateJSON(f.definition(), `{"objective":"remaining step"}`); repair == "" {
		t.Fatal("old task schema accepted at root")
	}
	after, _ := plan.GetTask(f.d.Store, prior.ID)
	if !reflect.DeepEqual(before, after) || after.Attempts != 0 {
		t.Fatal("navigation changed draft or claimed work")
	}
	// A separate explicit selection restores the actual saved question.
	bound, args, err = focus.Bind(t.Context(), tool.Args{"ref": prior.ID})
	if err != nil {
		t.Fatal(err)
	}
	res, err = bound.Execute(t.Context(), args)
	if err != nil || res.Focus.ID != prior.ID || strings.Contains(res.Text, "Root drafts") {
		t.Fatalf("return failed: %+v %v", res, err)
	}
	f.scope = res.Focus
	if f.definition().Schema() != question {
		t.Fatal("saved question changed")
	}
	f.call(tool.Args{"objective": "remaining step"})
	if f.definition().Params[0].Name != "execution" || f.judgments != 0 {
		t.Fatal("explicit return did not advance only the requested draft")
	}
}

func TestPlanningRootReturnDoesNotAdvertiseForeignOrMissingOwner(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	for _, tc := range []struct {
		name      string
		scope     session.WorkflowScope
		workspace string
	}{
		{"foreign-store", session.WorkflowScope{Store: "other", Kind: "task", ID: f.scope.ID}, f.d.Workspace},
		{"missing", session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "task", ID: "deleted"}, f.d.Workspace},
		{"foreign-project", *f.scope, t.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := f.d
			d.Workspace = tc.workspace
			focus := planningFocusReturn(d, &tc.scope, launch.FocusDefinitionAt(d.Store, config.Defaults().Tools.Bash, d.Workspace, d.Prompts))
			bound, args, err := focus.Bind(t.Context(), tool.Args{"ref": "root"})
			if err != nil {
				t.Fatal(err)
			}
			res, err := bound.Execute(t.Context(), args)
			if err != nil || res.Focus.Kind != "root" || strings.Contains(res.Text, "To continue") {
				t.Fatalf("wrong recovery authority: %+v %v", res, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := bound.Execute(ctx, args); err == nil {
				t.Fatal("navigation ignored cancellation")
			}
		})
	}
}

func TestPlanningRootReturnRechecksClosedAncestry(t *testing.T) {
	for _, state := range []string{plan.PlanComplete, plan.PlanRetired} {
		t.Run(state, func(t *testing.T) {
			f := newPlanningFixture(t)
			f.task()
			a, err := plan.TaskApproach(f.d.Store, f.scope.ID)
			if err != nil {
				t.Fatal(err)
			}
			focus := planningFocusReturn(f.d, f.scope, launch.FocusDefinitionAt(f.d.Store, config.Defaults().Tools.Bash, f.d.Workspace, f.d.Prompts))
			bound, args, err := focus.Bind(t.Context(), tool.Args{"ref": "root"})
			if err != nil {
				t.Fatal(err)
			}
			// Change authority after binding; no stale recovery advice may escape.
			if err = f.d.Store.Write(`MATCH(p:Plan)-[:HAS_APPROACH]->(a:Approach) WHERE a.id=$id SET p.state=$state`, map[string]any{"id": a.ID, "state": state}); err != nil {
				t.Fatal(err)
			}
			res, err := bound.Execute(t.Context(), args)
			if err != nil || res.Focus.Kind != "root" || strings.Contains(res.Text, "To continue") {
				t.Fatalf("closed owner advertised: %+v %v", res, err)
			}
		})
	}
}
