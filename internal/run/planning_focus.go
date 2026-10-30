package run

import (
	"context"

	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Root is a deliberate new-plan view, not a reset of the saved draft. Explain
// how to return using the request's existing owner; never select it implicitly.
func planningFocusReturn(d launch.Deps, previous *session.WorkflowScope, def tool.Definition) tool.Definition {
	if previous == nil || previous.ID == "" || def.Bind == nil {
		return def
	}
	prior := *previous
	bind := def.Bind
	def.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		bound, actual, err := bind(ctx, args)
		if err != nil {
			return bound, actual, err
		}
		execute := bound.Execute
		bound.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
			res, err := execute(ctx, args)
			if err != nil || res.Failed || res.Blocked || res.Focus == nil || res.Focus.Kind != "root" {
				return res, err
			}
			// Fresh reads avoid suggesting a deleted, foreign or closed owner.
			n, e := launch.ScopeNode(d.Store, &prior)
			if e != nil || n.Closed || n.Retired || plan.CheckProject(d.Store, prior.Kind, prior.ID, d.Workspace) != nil {
				return res, nil
			}
			pc := d.Prompts
			if !pc.Has("planning.focus_root_return") {
				pc = prompts.Builtin()
			}
			res.Text += "\n" + pc.Format("planning.focus_root_return", prior.Kind, prior.ID)
			return res, nil
		}
		return bound, actual, nil
	}
	return def
}
