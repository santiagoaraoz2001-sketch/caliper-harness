package launch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// NewBuild returns the workflow-construction tools: the small-model
// path from plan to fanned-out approach, one tiny call per step.
// Each op names its ids back so the model chains them without
// holding the whole graph in context.
func NewBuild(d Deps, configuredCommand ...string) []tool.Definition {
	if d.Store == nil {
		return nil
	}
	str := func(name, desc string, required bool) tool.Param {
		return tool.Param{Name: name, Desc: desc, Type: tool.TypeString, Required: required}
	}
	need := func(args tool.Args, name string) (string, error) {
		v, _ := args[name].(string)
		if v == "" {
			return "", fmt.Errorf("workflow needs %q — pass the id back from the earlier step", name)
		}
		return v, nil
	}
	focus := func(kind, id string) *tool.WorkflowScope {
		return &tool.WorkflowScope{Store: d.Store.Identity(), Kind: kind, ID: id}
	}
	command := ""
	if len(configuredCommand) > 0 {
		command = configuredCommand[0]
	}
	return []tool.Definition{
		{
			Name: "workflow_plan", Label: "workflow plan",
			Desc:    d.Prompts.Text("tools.workflow_plan.description"),
			Purpose: d.Prompts.Text("tools.workflow_plan.purpose"),
			Params: []tool.Param{
				str("title", d.Prompts.Text("tools.workflow_plan.parameters.title"), true),
				str("goal", d.Prompts.Text("tools.workflow_plan.parameters.goal"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				title, _ := args["title"].(string)
				goal, _ := args["goal"].(string)
				p, err := plan.CreatePlanContext(plan.WithProject(ctx, d.Workspace), d.Store, title, goal)
				if err != nil {
					return tool.Result{}, err
				}
				if d.ApproveContract != nil {
					if err = d.ApproveContract(ctx, "plan", p.ID); err != nil {
						return tool.Result{Created: &tool.CreatedNode{Kind: "plan", ID: p.ID, State: p.State}}, err
					}
				}
				return tool.Result{Created: &tool.CreatedNode{Kind: "plan", ID: p.ID, State: p.State}, Focus: focus("plan", p.ID), Text: fmt.Sprintf("created plan %s (%s).", p.ID, p.State) + d.Prompts.Format("continuation.workflow.created_plan", p.ID, p.ID)}, nil
			},
		},
		{
			Name: "workflow_approach", Label: "workflow approach",
			Desc:    d.Prompts.Text("tools.workflow_approach.description"),
			Purpose: d.Prompts.Text("tools.workflow_approach.purpose"),
			Params: []tool.Param{
				str("plan", d.Prompts.Text("tools.workflow_approach.parameters.plan"), true),
				str("title", d.Prompts.Text("tools.workflow_approach.parameters.title"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				pid, err := need(args, "plan")
				if err != nil {
					return tool.Result{}, err
				}
				title, _ := args["title"].(string)
				a, err := plan.CreateApproachContext(plan.WithProject(ctx, d.Workspace), d.Store, pid, title)
				if err != nil {
					return tool.Result{}, err
				}
				if d.ApproveContract != nil {
					if err = d.ApproveContract(ctx, "approach", a.ID); err != nil {
						return tool.Result{Created: &tool.CreatedNode{Kind: "approach", ID: a.ID, State: a.State}}, err
					}
				}
				return tool.Result{Created: &tool.CreatedNode{Kind: "approach", ID: a.ID, State: a.State}, Focus: focus("approach", a.ID), Text: fmt.Sprintf("created approach %s (%s) under plan %s.", a.ID, a.State, pid) + d.Prompts.Format("continuation.workflow.created_approach", a.ID)}, nil
			},
		},
		{
			Name: "workflow_task", Label: "workflow task",
			Desc:    d.Prompts.Text("tools.workflow_task.description"),
			Purpose: d.Prompts.Text("tools.workflow_task.purpose"),
			Params: []tool.Param{
				str("approach", d.Prompts.Text("tools.workflow_task.parameters.approach"), true),
				str("title", d.Prompts.Text("tools.workflow_task.parameters.title"), true),
				str("prompt", d.Prompts.Text("tools.workflow_task.parameters.prompt"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				aid, err := need(args, "approach")
				if err != nil {
					return tool.Result{}, err
				}
				title, _ := args["title"].(string)
				prompt, _ := args["prompt"].(string)
				t, err := plan.CreateTaskContext(plan.WithProject(ctx, d.Workspace), d.Store, aid, title, prompt)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Result{Created: &tool.CreatedNode{Kind: "task", ID: t.ID, State: t.State}, Focus: focus("task", t.ID), Text: fmt.Sprintf("created task %s (%s) under approach %s.", t.ID, t.State, aid) + d.Prompts.Format("continuation.workflow.created_task", t.ID)}, nil
			},
		},
		{
			Name: "workflow_block", Label: "workflow block",
			Desc:    d.Prompts.Text("tools.workflow_block.description"),
			Purpose: d.Prompts.Text("tools.workflow_block.purpose"),
			Params: []tool.Param{
				str("task", d.Prompts.Text("tools.workflow_block.parameters.task"), true),
				str("current", d.Prompts.Text("tools.workflow_block.parameters.current"), true),
				str("target", d.Prompts.Text("tools.workflow_block.parameters.target"), true),
				str("sketch", d.Prompts.Text("tools.workflow_block.parameters.sketch"), true),
				str("acceptance", d.Prompts.Text("tools.workflow_block.parameters.acceptance"), true),
				str("tests", d.Prompts.Text("tools.workflow_block.parameters.tests"), true),
				str("verify", d.Prompts.Text("tools.workflow_block.parameters.verify"), true),
				str("acceptance_command", d.Prompts.Text("tools.workflow_block.parameters.acceptance_command"), false),
				str("check_command", d.Prompts.Text("tools.workflow_block.parameters.check_command"), false),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				id, err := need(args, "task")
				if err != nil {
					return tool.Result{}, err
				}
				if err := plan.CheckProject(d.Store, "task", id, d.Workspace); err != nil {
					return tool.Result{}, err
				}
				get := func(k string) string { v, _ := args[k].(string); return v }
				var check, behavior *string
				if v, ok := args["check_command"]; ok {
					value := v.(string)
					if strings.TrimSpace(value) == "" || command != "" && value != command {
						return tool.Result{}, fmt.Errorf("check_command must be nonblank and match configured command %q; omit to bind that configured command", command)
					}
					check = &value
				} else if command != "" {
					check = &command
				}
				if v, ok := args["acceptance_command"]; ok {
					value := v.(string)
					behavior = &value
				}
				if d.Wf.VerifyFollowup {
					t, err := plan.GetTask(d.Store, id)
					if err != nil {
						return tool.Result{}, err
					}
					if check != nil {
						t.CheckCommand = *check
					}
					if behavior != nil {
						t.AcceptanceCommand = *behavior
					}
					if err := requiredCommands(d, []*plan.Task{t}); err != nil {
						return tool.Result{}, err
					}
				}
				_, err = plan.SetTaskBlockCommandsContext(plan.WithProject(ctx, d.Workspace), d.Store, id,
					get("current"), get("target"), get("sketch"),
					get("acceptance"), get("tests"), get("verify"), check, behavior)
				if err != nil {
					return tool.Result{}, err
				}
				if d.ApproveContract != nil {
					if err = d.ApproveContract(ctx, "task", id); err != nil {
						return tool.Result{}, err
					}
				}
				return tool.Result{Text: "task " + id + d.Prompts.Text("continuation.workflow.saved_task_brief")}, nil
			},
		},
		{
			Name: "workflow_ready", Label: "workflow ready", Desc: d.Prompts.Text("tools.workflow_ready.description"),
			Purpose: d.Prompts.Text("tools.workflow_ready.purpose"),
			Params:  []tool.Param{str("approach", d.Prompts.Text("tools.workflow_ready.parameters.approach"), true)},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				id, err := need(args, "approach")
				if err != nil {
					return tool.Result{}, err
				}
				a, err := Ready(plan.WithProject(ctx, d.Workspace), d, id)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Result{ReadyNode: &tool.CreatedNode{Kind: "approach", ID: a.ID, State: a.State}, Text: "approach " + a.ID + " " + a.State + d.Prompts.Format("continuation.workflow.ready_next", a.ID)}, nil
			},
		},
		{
			Name: "workflow_transition", Label: "workflow transition", Desc: d.Prompts.Text("tools.workflow_transition.description"),
			Purpose: d.Prompts.Text("tools.workflow_transition.purpose"),
			Params: []tool.Param{
				{Name: "kind", Desc: d.Prompts.Text("tools.workflow_transition.parameters.kind"), Type: tool.TypeString, Required: true, Enum: []string{"plan", "task"}},
				str("id", d.Prompts.Text("tools.workflow_transition.parameters.id"), true),
				{Name: "to", Desc: d.Prompts.Text("tools.workflow_transition.parameters.to"), Type: tool.TypeString, Required: true, Enum: []string{"active", "complete", "failed", "queued"}},
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				id, err := need(args, "id")
				if err != nil {
					return tool.Result{}, err
				}
				kind, _ := args["kind"].(string)
				to, _ := args["to"].(string)
				if err := plan.CheckProject(d.Store, kind, id, d.Workspace); err != nil {
					return tool.Result{}, err
				}
				if kind == "plan" && (to == plan.PlanActive || to == plan.PlanComplete) {
					if d.ApproveContract != nil {
						if err = d.ApproveContract(ctx, "plan", id); err != nil {
							return tool.Result{}, err
						}
					}
					p, err := plan.TransitionPlanContext(plan.WithProject(ctx, d.Workspace), d.Store, id, to)
					if err != nil {
						return tool.Result{}, err
					}
					return tool.Result{Text: "plan " + p.ID + " " + p.State + d.Prompts.Text("continuation.workflow.plan_transition")}, nil
				}
				if kind == "task" && (to == plan.TaskFailed || to == plan.TaskQueued) {
					t, err := plan.TransitionTaskContext(plan.WithProject(ctx, d.Workspace), d.Store, id, to)
					if err != nil {
						return tool.Result{}, err
					}
					return tool.Result{Text: "task " + t.ID + " " + t.State + d.Prompts.Text("continuation.workflow.task_rework")}, nil
				}
				return tool.Result{}, fmt.Errorf("workflow_transition refuses %s→%s; plan accepts active/complete, task accepts failed/queued", kind, to)
			},
		},
		{
			Name: "workflow_status", Label: "workflow status",
			Desc:    d.Prompts.Text("tools.workflow_status.description"),
			Purpose: d.Prompts.Text("tools.workflow_status.purpose"),
			Params: []tool.Param{
				str("approach", d.Prompts.Text("tools.workflow_status.parameters.approach"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				aid, err := need(args, "approach")
				if err != nil {
					return tool.Result{}, err
				}
				if err := plan.CheckProject(d.Store, "approach", aid, d.Workspace); err != nil {
					return tool.Result{}, err
				}
				tasks, err := plan.ApproachTasks(d.Store, aid)
				if err != nil {
					return workflowResult(d, aid, nil, err)
				}
				results := make([]Result, len(tasks))
				for i, t := range tasks {
					results[i] = resultOf(t)
				}
				result, err := workflowResult(d, aid, results, nil)
				// Observation success is independent of the inspected work's state.
				result.Failed = err != nil
				return result, err
			},
		},
		{
			Name: "workflow_pause", Label: "workflow pause",
			Desc:    d.Prompts.Text("tools.workflow_pause.description"),
			Purpose: d.Prompts.Text("tools.workflow_pause.purpose"),
			Params: []tool.Param{
				str("approach", d.Prompts.Text("tools.workflow_pause.parameters.approach"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				aid, err := need(args, "approach")
				if err != nil {
					return tool.Result{}, err
				}
				if err := plan.CheckProject(d.Store, "approach", aid, d.Workspace); err != nil {
					return tool.Result{}, err
				}
				if err := Pause(d.Store, aid); err != nil {
					return tool.Result{}, err
				}
				return tool.Result{Text: "approach " + aid + d.Prompts.Text("continuation.workflow.paused_approach")}, nil
			},
		},
		{
			Name: "workflow_retire", Label: "workflow retire",
			Desc:    d.Prompts.Text("tools.workflow_retire.description"),
			Purpose: d.Prompts.Text("tools.workflow_retire.purpose"),
			Params: []tool.Param{
				str("target", d.Prompts.Text("tools.workflow_retire.parameters.target"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				id, err := need(args, "target")
				if err != nil {
					return tool.Result{}, err
				}
				if _, err := plan.GetPlan(d.Store, id); err == nil {
					p, err := plan.TransitionPlanContext(plan.WithProject(ctx, d.Workspace), d.Store, id, plan.PlanRetired)
					if err != nil {
						return tool.Result{}, err
					}
					return tool.Result{Text: "retired plan " + p.ID}, nil
				} else if !errors.Is(err, plan.ErrNotFound) {
					return tool.Result{}, err
				}
				a, err := plan.TransitionApproachCheckedContext(plan.WithProject(ctx, d.Workspace), d.Store, id, plan.ApproachRetired, "")
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Result{Text: "retired approach " + a.ID}, nil
			},
		},
		{
			Name: "workflow_resume", WorkflowRefArg: "approach", ResolveWorkflowRef: workflowRef(d), Label: "workflow resume",
			Desc:    d.Prompts.Text("tools.workflow_resume.description"),
			Purpose: d.Prompts.Text("tools.workflow_resume.purpose"),
			Params: []tool.Param{
				str("approach", d.Prompts.Text("tools.workflow_resume.parameters.approach"), true),
			},
			Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if err := ctx.Err(); err != nil {
					return tool.Result{}, err
				}
				aid, err := need(args, "approach")
				if err != nil {
					return tool.Result{}, err
				}
				if err := plan.CheckProject(d.Store, "approach", aid, d.Workspace); err != nil {
					return tool.Result{}, err
				}
				// Unchanged paused work may resume its admitted brief. Edited
				// work refuses until paused→ready rechecks readiness.
				results, err := Resume(ctx, d, aid)
				return workflowResult(d, aid, results, err)
			},
		},
	}
}

// AuthMode gates model-initiated fan-out: anything but yolo asks the
// user first. Direct invocations (CLI, TUI keys) carry user intent
// already and leave Approve nil.
func approveLaunch(ctx context.Context, d Deps, approachID string, n int) error {
	if d.AuthMode == "" || d.AuthMode == config.AuthYOLO || d.AuthMode == config.AuthRisky && d.ApproveContract != nil {
		return nil
	}
	if auth.Approved(ctx, "launch", tool.Args{"approach": approachID}) || auth.Approved(ctx, "workflow_resume", tool.Args{"approach": approachID}) {
		return nil
	}
	if d.Approve == nil {
		return fmt.Errorf("launch needs user approval — no approver wired")
	}
	tasks, err := plan.ApproachTasks(d.Store, approachID)
	if err != nil {
		return err
	}
	var commands strings.Builder
	for _, t := range tasks {
		if strings.TrimSpace(t.CheckCommand) != "" {
			fmt.Fprintf(&commands, "\n%s acceptance command (first):\n%s\nconfigured check (last):\n%s", t.Title, t.AcceptanceCommand, t.CheckCommand)
		}
	}
	if !d.Approve(ctx, fmt.Sprintf("launch approach %s (%d tasks)%s?", approachID, n, commands.String())) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("launch refused by the user")
	}
	return nil
}

// ApproveLaunch applies the same dispatcher approval to direct CLI/TUI entries.
func ApproveLaunch(ctx context.Context, d Deps, id string) error {
	tasks, err := plan.ApproachTasks(d.Store, id)
	if err != nil {
		return err
	}
	return approveLaunch(ctx, d, id, len(tasks))
}
