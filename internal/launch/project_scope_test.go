package launch

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func projectWorkflowFixture(t *testing.T) (*store.Store, *plan.Plan, *plan.Approach, *plan.Task, Deps) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), root), s, "private project", "private goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "private approach")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTask(s, a.ID, "private task", "private task prompt")
	if err != nil {
		t.Fatal(err)
	}
	// Compare persisted values, including the store's timestamp precision.
	p, err = plan.GetPlan(s, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, err = plan.GetApproach(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	x, err = plan.GetTask(s, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := depsFor(s, &echoModel{})
	d.Workspace, d.Prompts = root, prompts.Builtin()
	return s, p, a, x, d
}

func workflowTool(t *testing.T, d Deps, name string) tool.Definition {
	t.Helper()
	if name == "launch" {
		return New(d)
	}
	for _, def := range NewBuild(d) {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("missing workflow tool %s", name)
	return tool.Definition{}
}

func TestWorkflowToolsRejectAnotherProjectBeforeEffects(t *testing.T) {
	s, p, a, x, d := projectWorkflowFixture(t)
	d.Workspace = t.TempDir()
	sideEffects := 0
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		sideEffects++
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	d.ApproveContract = func(context.Context, string, string) error { sideEffects++; return nil }
	d.Approve = func(context.Context, string) bool { sideEffects++; return true }
	cases := []struct {
		name string
		args tool.Args
	}{
		{"workflow_approach", tool.Args{"plan": p.ID, "title": "intrusion"}},
		{"workflow_task", tool.Args{"approach": a.ID, "title": "intrusion", "prompt": "intrusion"}},
		{"workflow_block", tool.Args{"task": x.ID, "current": "intrusion"}},
		{"workflow_ready", tool.Args{"approach": a.ID}},
		{"workflow_transition", tool.Args{"kind": "plan", "id": p.ID, "to": plan.PlanActive}},
		{"workflow_transition", tool.Args{"kind": "task", "id": x.ID, "to": plan.TaskFailed}},
		{"workflow_status", tool.Args{"approach": a.ID}},
		{"workflow_pause", tool.Args{"approach": a.ID}},
		{"workflow_retire", tool.Args{"target": p.ID}},
		{"workflow_retire", tool.Args{"target": a.ID}},
		{"workflow_resume", tool.Args{"approach": a.ID}},
		{"launch", tool.Args{"approach": a.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := workflowTool(t, d, tc.name)
			r, err := def.Execute(t.Context(), tc.args)
			if err == nil || !strings.Contains(err.Error(), "outside captured project") {
				t.Fatalf("foreign operation: result=%+v error=%v", r, err)
			}
			if r.Text != "" || r.Created != nil || r.Focus != nil || r.Workflow != nil {
				t.Fatalf("foreign data or success receipt returned: %+v", r)
			}
			if def.ResolveWorkflowRef != nil {
				id, err := def.ResolveWorkflowRef(t.Context(), tc.args)
				if err == nil {
					t.Fatalf("foreign workflow obligation: id=%q error=%v", id, err)
				}
			}
			gotP, _ := plan.GetPlan(s, p.ID)
			gotA, _ := plan.GetApproach(s, a.ID)
			gotX, _ := plan.GetTask(s, x.ID)
			children, _ := plan.PlanApproaches(s, p.ID)
			tasks, _ := plan.ApproachTasks(s, a.ID)
			if !reflect.DeepEqual(p, gotP) || !reflect.DeepEqual(a, gotA) || !reflect.DeepEqual(x, gotX) || len(children) != 1 || len(tasks) != 1 || sideEffects != 0 {
				t.Fatalf("foreign state, judge or approval changed: plan=%+v approach=%+v task=%+v effects=%d", gotP, gotA, gotX, sideEffects)
			}
		})
	}
}

func TestWorkflowFocusProjectCheckBeforeBriefAndAtExecution(t *testing.T) {
	s, p, a, x, d := projectWorkflowFixture(t)
	cfg := config.Defaults().Tools.Bash
	foreign := FocusDefinitionAt(s, cfg, t.TempDir())
	for _, id := range []string{p.ID, a.ID, x.ID} {
		_, _, err := bindFocus(t, foreign, map[string]any{"ref": id, "offset": 0, "limit": 20})
		if err == nil || !strings.Contains(err.Error(), "outside captured project") {
			t.Fatalf("foreign brief %s: %v", id, err)
		}
	}
	local := FocusDefinitionAt(s, cfg, d.Workspace)
	bound, args, err := bindFocus(t, local, map[string]any{"ref": x.ID, "offset": 0, "limit": 20})
	if err != nil {
		t.Fatal(err)
	}
	res, err := bound.Execute(t.Context(), args)
	if err != nil || !strings.Contains(res.Text, "private task prompt") {
		t.Fatalf("same-project brief: %+v %v", res, err)
	}
	// A concurrent raw-store mutation models invalidated ownership between
	// preflight and execution. The public owner never permits moving a plan.
	if err := s.Write("MATCH(p:Plan) WHERE p.id=$id SET p.project_root=$root", map[string]any{"id": p.ID, "root": "other"}); err != nil {
		t.Fatal(err)
	}
	if res, err = bound.Execute(t.Context(), args); err == nil || res.Text != "" {
		t.Fatalf("stale focus leaked brief: %+v %v", res, err)
	}
	// Trusted host navigation retains its existing unscoped contract.
	host, hostArgs, err := bindFocus(t, FocusDefinition(s, cfg), map[string]any{"ref": x.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Execute(t.Context(), hostArgs); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowToolsKeepSameProjectLifecycle(t *testing.T) {
	s, p, a, x, d := projectWorkflowFixture(t)
	call := func(name string, args tool.Args) tool.Result {
		t.Helper()
		r, err := workflowTool(t, d, name).Execute(t.Context(), args)
		if err != nil {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		return r
	}
	newPlan := call("workflow_plan", tool.Args{"title": "second", "goal": "second"})
	if err := plan.CheckProject(s, "plan", newPlan.Created.ID, d.Workspace); err != nil {
		t.Fatal(err)
	}
	newApproach := call("workflow_approach", tool.Args{"plan": newPlan.Created.ID, "title": "second"})
	newTask := call("workflow_task", tool.Args{"approach": newApproach.Created.ID, "title": "second", "prompt": "second"})
	if err := plan.CheckProject(s, "task", newTask.Created.ID, d.Workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, newTask.Created.ID, plan.TaskCancelled); err != nil {
		t.Fatal(err)
	}
	call("workflow_transition", tool.Args{"kind": "task", "id": newTask.Created.ID, "to": plan.TaskQueued})
	call("workflow_block", tool.Args{"task": x.ID, "current": "now", "target": "after", "sketch": "mechanism", "acceptance": "exit 0", "tests": "TestX", "verify": "run check", "check_command": "printf SUITE", "acceptance_command": "printf BEHAVIOR"})
	call("workflow_transition", tool.Args{"kind": "plan", "id": p.ID, "to": plan.PlanActive})
	call("workflow_ready", tool.Args{"approach": a.ID})
	status := call("workflow_status", tool.Args{"approach": a.ID})
	if status.Workflow == nil || status.Workflow.Qualified {
		t.Fatalf("unfinished workflow status misrepresented: %+v", status)
	}
	id, err := workflowRef(d)(t.Context(), tool.Args{"approach": a.ID})
	if err != nil || id != a.ID {
		t.Fatalf("same-project reference %q %v", id, err)
	}
	if _, err := plan.TransitionApproach(s, a.ID, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	call("workflow_pause", tool.Args{"approach": a.ID})
	// Empty paused work resumes and settles with its actual unverified outcome;
	// this asserts project binding does not obstruct the existing host pipeline.
	r, err := workflowTool(t, d, "workflow_resume").Execute(t.Context(), tool.Args{"approach": a.ID})
	if r.Workflow == nil || strings.Contains(r.Text, "outside captured project") {
		t.Fatalf("same-project resume refused by scope: %+v %v", r, err)
	}
	// Scope permits the call, but cannot bypass the normal lifecycle.
	for _, id := range []string{newApproach.Created.ID, newPlan.Created.ID} {
		_, err := workflowTool(t, d, "workflow_retire").Execute(t.Context(), tool.Args{"target": id})
		if err == nil || strings.Contains(err.Error(), "outside captured project") {
			t.Fatalf("draft retirement must reach the existing lifecycle refusal: %v", err)
		}
	}
}

func TestWorkflowSameProjectLaunchCheckReviewAndRetire(t *testing.T) {
	s, aid, _ := setup(t, 1)
	d := depsFor(s, &outcomeModel{reply: "scoped fixture artifact", review: "PASS: lifecycle component"})
	d.Prompts = prompts.Builtin()
	d.Workspace = bindFixtureProject(t, s, aid, t.TempDir())
	r, err := New(d).Execute(t.Context(), tool.Args{"approach": aid})
	if err != nil || r.Workflow == nil || !r.Workflow.Qualified {
		t.Fatalf("same-project check/review pipeline: %+v %v", r, err)
	}
	p, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionPlan(s, p.ID, plan.PlanComplete); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{aid, p.ID} {
		if _, err := workflowTool(t, d, "workflow_retire").Execute(t.Context(), tool.Args{"target": id}); err != nil {
			t.Fatalf("same-project completed retirement %s: %v", id, err)
		}
	}
}
