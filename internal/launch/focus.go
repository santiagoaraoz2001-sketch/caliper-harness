package launch

import (
	"context"
	"fmt"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"unicode/utf8"
)

// SelectScope never infers an owner from browsing, creation or active state.
func SelectScope(s *store.Store, ref string) (*session.WorkflowScope, error) {
	identity := s.Identity()
	if identity == "" {
		return nil, fmt.Errorf("workflow scope needs an open persistent store")
	}
	if ref == "root" {
		return &session.WorkflowScope{Store: identity, Kind: "root"}, nil
	}
	nodes, err := plan.References(s)
	if err != nil {
		return nil, err
	}
	n, err := plan.ResolveReference(nodes, ref)
	if err != nil {
		return nil, err
	}
	return &session.WorkflowScope{Store: identity, Kind: n.Kind, ID: n.ID}, nil
}

// ScopeNode re-fetches identity/state; continuation contains no copied graph authority.
func ScopeNode(s *store.Store, scope *session.WorkflowScope) (plan.Reference, error) {
	if scope == nil || scope.Store == "" || scope.Store != s.Identity() {
		return plan.Reference{}, fmt.Errorf("workflow scope belongs to a different or closed store; explicitly /workflow again")
	}
	nodes, err := plan.References(s)
	if err != nil {
		return plan.Reference{}, err
	}
	if scope.Kind == "root" && scope.ID == "" {
		count := 0
		for _, n := range nodes {
			if n.Kind == "plan" && !n.Retired {
				count++
			}
		}
		return plan.Reference{Kind: "root", Ref: "root", Children: count}, nil
	}
	n, err := plan.ResolveReference(nodes, scope.ID)
	if err == nil && n.Kind != scope.Kind {
		err = fmt.Errorf("workflow scope kind changed")
	}
	return n, err
}

func scopeOps(s *store.Store, n plan.Reference) (map[string]tool.Args, error) {
	ops := map[string]tool.Args{"workflow_focus": nil, "ask": nil}
	add := func(name, key string) { ops[name] = tool.Args{key: n.ID} }
	switch n.Kind {
	case "root":
		ops["workflow_plan"] = nil
	case "plan":
		ops["check"] = nil
		if n.State == plan.PlanDraft || n.State == plan.PlanActive {
			add("workflow_approach", "plan")
			ops["workflow_transition"] = tool.Args{"kind": "plan", "id": n.ID}
		}
		if n.State == plan.PlanComplete {
			add("workflow_retire", "target")
		}
	case "approach":
		add("workflow_status", "approach")
		switch {
		case n.State == plan.ApproachComplete:
			add("workflow_retire", "target")
		case n.Closed:
		case n.State == plan.ApproachDraft || n.State == plan.ApproachPaused:
			add("workflow_task", "approach")
			add("workflow_ready", "approach")
			a, err := plan.GetApproach(s, n.ID)
			if err != nil {
				return nil, err
			}
			if n.State == plan.ApproachPaused && !a.NeedsReady {
				add("workflow_resume", "approach")
			}
		case n.State == plan.ApproachReady:
			add("launch", "approach")
		case n.State == plan.ApproachRunning:
			add("workflow_pause", "approach")
		}
	case "task":
		a, err := plan.GetApproach(s, n.Parent)
		if err != nil {
			return nil, err
		}
		if !n.Closed && (a.State == plan.ApproachDraft || a.State == plan.ApproachPaused) && (n.State == plan.TaskQueued || n.State == plan.TaskFailed || n.State == plan.TaskStopped || n.State == plan.TaskCancelled) {
			add("workflow_block", "task")
		}
		if n.State == plan.TaskDone || !n.Closed && (n.State == plan.TaskFailed || n.State == plan.TaskStopped || n.State == plan.TaskCancelled) {
			ops["workflow_transition"] = tool.Args{"kind": "task", "id": n.ID}
		}
	}
	return ops, nil
}

// FocusView freezes definitions and hidden targets for one generation/array.
// Optional final projections run before the header is rendered from that view.
func FocusView(reg *tool.Registry, s *store.Store, scope *session.WorkflowScope, projections ...func(*tool.Registry) (*tool.Registry, error)) (*tool.Registry, string, error) {
	if scope == nil {
		return nil, "", fmt.Errorf("workflow scope is missing")
	}
	owned := *scope
	scope = &owned
	n, err := ScopeNode(s, scope)
	if err != nil {
		return nil, "", err
	}
	ops, err := scopeOps(s, n)
	if err != nil {
		return nil, "", err
	}
	view := tool.NewRegistry(reg.Prompts)
	for name, hidden := range ops {
		original, ok := reg.Lookup(name)
		if !ok {
			return nil, "", fmt.Errorf("workflow owning tool %s is not registered", name)
		}
		if execute := original.Execute; execute != nil {
			original.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
				res, err := execute(ctx, args)
				if err == nil && res.Created != nil {
					n := res.Created
					res.Text = fmt.Sprintf("created %s %s (%s). Metadata only; no child execution.", n.Kind, n.ID, n.State) + reg.Prompts.Format("continuation.workflow.scoped_created", n.ID)
				} else if err == nil && !res.Failed && !res.Blocked {
					switch name {
					case "workflow_ready":
						if res.ReadyNode != nil {
							res.Text = "approach " + res.ReadyNode.ID + " " + res.ReadyNode.State + reg.Prompts.Text("continuation.workflow.scoped_ready_next")
						}
					case "workflow_block":
						res.Text = "task " + n.ID + reg.Prompts.Format("continuation.workflow.scoped_saved_task_brief", n.Parent)
					}
				}
				return res, err
			}
		}
		def := original
		def.Params = nil
		for _, p := range original.Params {
			if _, bound := hidden[p.Name]; bound {
				continue
			}
			if name == "workflow_transition" && p.Name == "to" {
				next, err := plan.NextTask(n.State)
				if n.Kind == "plan" {
					next, err = plan.NextPlan(n.State)
				}
				if err != nil {
					return nil, "", err
				}
				p.Enum = nil
				for _, state := range next {
					if state == "active" || state == "complete" || state == "failed" || state == "queued" {
						p.Enum = append(p.Enum, state)
					}
				}
			}
			def.Params = append(def.Params, p)
		}
		if len(def.Params) == 0 {
			def.Params = []tool.Param{{Name: "invoke", Type: tool.TypeString, Required: true, ExactEnum: true, Enum: []string{"run"}, Desc: reg.Prompts.Text("tools.workflow_scoped.parameters.invoke")}}
		}
		if len(hidden) > 0 {
			if name == "workflow_transition" {
				def.Purpose = reg.Prompts.Format("system.workflow.scoped_transition", n.Kind)
			} else if name == "launch" {
				def.Purpose = reg.Prompts.Text("system.workflow.scoped_launch")
			}
			slots := make([]string, len(def.Params))
			for i, p := range def.Params {
				slots[i] = p.Name
			}
			def.Purpose += reg.Prompts.Format("system.workflow.selected_arguments", n.Kind, n.ID, strings.Join(slots, ", "))
			if len(def.Params) == 1 && def.Params[0].Name == "invoke" {
				def.Purpose += reg.Prompts.Text("system.workflow.invoke_marker")
			}
		}
		if name == "workflow_plan" || name == "workflow_approach" || name == "workflow_task" {
			def.Purpose += reg.Prompts.Text("system.workflow.creation_focus")
		}
		def.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
			if err := ctx.Err(); err != nil {
				return original, nil, err
			}
			current, err := ScopeNode(s, scope)
			if err != nil {
				return original, nil, err
			}
			allowed, err := scopeOps(s, current)
			if err != nil {
				return original, nil, err
			}
			if _, ok := allowed[name]; !ok {
				return original, nil, fmt.Errorf("%s no longer legal; refetch selected scope", name)
			}
			expanded := tool.Args{}
			for k, v := range args {
				if k != "invoke" {
					expanded[k] = v
				}
			}
			for k, v := range hidden {
				expanded[k] = v
			}
			if original.Bind != nil {
				return original.Bind(ctx, expanded)
			}
			return original, expanded, nil
		}
		view.Register(def)
	}
	for _, project := range projections {
		view, err = project(view)
		if err != nil {
			return nil, "", err
		}
	}
	header := reg.Prompts.Format("system.workflow.scope_header", n.Kind, n.Ref, n.ID, n.State, n.Ancestors, n.Children, strings.Join(view.Names(), ", "))
	return view, header, nil
}

// FocusDefinition validates selection and an optional explicit brief page before effects.
func FocusDefinition(s *store.Store, cap config.BashConfig, catalogs ...*prompts.Catalog) tool.Definition {
	return FocusDefinitionAt(s, cap, "", catalogs...)
}

// FocusDefinitionAt binds model navigation to the host's captured project.
// The unscoped wrapper remains available to trusted host callers.
func FocusDefinitionAt(s *store.Store, cap config.BashConfig, root string, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	d := tool.Definition{Name: "workflow_focus", Desc: pc.Text("tools.workflow_focus.description"), Params: []tool.Param{
		{Name: "ref", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.workflow_focus.parameters.ref")},
		{Name: "offset", Type: tool.TypeInt, Desc: pc.Text("tools.workflow_focus.parameters.offset")},
		{Name: "limit", Type: tool.TypeInt, Desc: pc.Text("tools.workflow_focus.parameters.limit")},
	}}
	d.Execute = func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{}, fmt.Errorf("workflow_focus requires owning preflight; invoke through Runner")
	}
	d.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		if err := ctx.Err(); err != nil {
			return d, nil, err
		}
		scope, err := SelectScope(s, args["ref"].(string))
		if err != nil {
			return d, nil, err
		}
		if scope.ID != "" {
			if err := plan.CheckProject(s, scope.Kind, scope.ID, root); err != nil {
				return d, nil, err
			}
			args["ref"] = scope.ID
		}
		_, offset := args["offset"]
		pageRequested := offset || args["limit"] != nil
		if pageRequested {
			_, err := briefPage(s, scope, args, cap)
			if err != nil {
				return d, nil, err
			}
		}
		bound := d
		bound.Bind = nil
		bound.Execute = func(ctx context.Context, _ tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			if _, err := ScopeNode(s, scope); err != nil {
				return tool.Result{}, err
			}
			if scope.ID != "" {
				if err := plan.CheckProject(s, scope.Kind, scope.ID, root); err != nil {
					return tool.Result{}, err
				}
			}
			text := "workflow focus for next request: " + scope.Kind + " " + scope.ID
			if pageRequested {
				text, err = briefPage(s, scope, args, cap)
				if err != nil {
					return tool.Result{}, err
				}
			}
			return tool.Result{Focus: scope, Text: text}, nil
		}
		return bound, args, nil
	}
	return d
}
func briefPage(s *store.Store, scope *session.WorkflowScope, args tool.Args, cap config.BashConfig) (string, error) {
	offset, _ := args["offset"].(int64)
	limit, ok := args["limit"].(int64)
	if !ok {
		limit = int64(cap.MaxLines)
	}
	if offset < 0 || limit <= 0 || cap.MaxLines <= 0 || cap.MaxBytes <= 0 {
		return "", fmt.Errorf("brief page needs offset>=0, limit>0 and configured positive caps")
	}
	limit = min(limit, int64(cap.MaxLines))
	var body string
	switch scope.Kind {
	case "plan":
		p, err := plan.GetPlan(s, scope.ID)
		if err != nil {
			return "", err
		}
		body = "title:\n" + p.Title + "\ngoal:\n" + p.Goal
	case "approach":
		a, err := plan.GetApproach(s, scope.ID)
		if err != nil {
			return "", err
		}
		body = "title:\n" + a.Title
	case "task":
		t, err := plan.GetTask(s, scope.ID)
		if err != nil {
			return "", err
		}
		body = "title:\n" + t.Title + "\nprompt:\n" + t.Prompt + "\ncurrent:\n" + t.Current + "\ntarget:\n" + t.Target + "\nsketch:\n" + t.Sketch + "\nacceptance:\n" + t.Acceptance + "\ntests:\n" + t.Tests + "\nverify:\n" + t.Verify + "\ncheck_command:\n" + t.CheckCommand + "\nacceptance_command:\n" + t.AcceptanceCommand
	default:
		return "", fmt.Errorf("root has no brief; select a node explicitly")
	}
	lines := strings.Split(body, "\n")
	if offset >= int64(len(lines)) {
		return "", fmt.Errorf("brief offset beyond %d lines", len(lines))
	}
	end := int(offset) + int(min(limit, int64(len(lines))-offset))
	footer := func(next int) string {
		if next < len(lines) {
			return fmt.Sprintf("\n[brief incomplete; next offset=%d; total lines=%d]", next, len(lines))
		}
		return "\n[brief complete]"
	}
	header := fmt.Sprintf("Authoritative brief %s %q; offset=%d\n", scope.Kind, scope.ID, offset)
	maximum := end
	end = int(offset)
	used := len(header)
	for end < maximum {
		add := len(lines[end])
		if end > int(offset) {
			add++
		}
		if used+add+len(footer(end+1)) > cap.MaxBytes {
			break
		}
		used += add
		end++
	}
	if end == int(offset) {
		return "", fmt.Errorf("brief line %d exceeds byte cap; no line returned or cursor advanced", offset+1)
	}
	text := header + strings.Join(lines[int(offset):end], "\n") + footer(end)
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("brief contains invalid UTF-8")
	}
	return text, nil
}
