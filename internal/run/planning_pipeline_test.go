package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func draftPipelineTask(t *testing.T, f *planningFixture) string {
	t.Helper()
	id := f.scope.ID
	f.call(tool.Args{"current": "observed", "target": "desired"})
	f.call(tool.Args{"acceptance": "specific assertion", "tests": "regression", "acceptance_command": "true"})
	f.call(tool.Args{"objective": "bounded change"})
	f.call(tool.Args{"execution": "edit implementation and regression"})
	f.call(tool.Args{"validation": "run declared regression"})
	f.call(tool.Args{"more": false})
	if st, _ := planningRead(f.d, f.scope); st.Phase != "review" {
		t.Fatal(st.Phase)
	}
	task, _ := plan.GetTask(f.d.Store, id)
	if task.Attempts != 0 {
		t.Fatal("drafting claimed a task")
	}
	f.call(tool.Args{"verify": "check requested behavior and no regression"})
	return id
}

// Real graph, bindings, claims and lifecycle; explicit synthetic check/reviewer
// receipts isolate navigation. This is not evidence of a native-model execution.
func qualifyPipelineTask(t *testing.T, f *planningFixture, want string) {
	t.Helper()
	def := f.definition()
	_, args, err := def.Bind(context.Background(), tool.Args{"invoke": "EXECUTE"})
	if err != nil || args["task"] != want {
		t.Fatalf("next owner: %v %v", args, err)
	}
	a, _ := plan.GetApproach(f.d.Store, f.scope.ID)
	snapshot, err := plan.ReadinessSnapshot(f.d.Store, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.ClaimTaskContext(plan.WithProject(context.Background(), f.d.Workspace), f.d.Store, want, snapshot, "true")
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	log, _ := json.Marshal([]any{
		map[string]any{"Kind": "check", "Attempt": task.Attempts, "Command": task.CheckCommand, "Cwd": f.d.Workspace, "Started": "fixture-start", "Ended": "fixture-end", "episode_started": "fixture-start", "episode_ended": "fixture-end", "ExitCode": 0, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed, "acceptance": session.CommandReceipt{Command: task.AcceptanceCommand, Cwd: f.d.Workspace, Started: "fixture-start", Ended: "fixture-end", Status: "passed", ExitCode: &zero}},
		map[string]any{"Kind": "review", "Attempt": task.Attempts, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed},
	})
	task.AttemptLog, task.Execution, task.Verification = string(log), outcome.Completed, outcome.Passed
	task.Review = "PASS: synthetic navigation fixture"
	if err := plan.SettleTaskOutcome(f.d.Store, task, plan.TaskDone); err != nil {
		t.Fatal(err)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "task", ID: want}
	if st, _ := planningRead(f.d, f.scope); st.Phase != "task_complete" {
		t.Fatal(st.Phase)
	}
	f.call(tool.Args{"invoke": "NEXT"})
}

func TestGuidedPlanningTwoApproachesTwoTasks(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	for approach := 0; approach < 2; approach++ {
		first := draftPipelineTask(t, f)
		aid := f.scope.ID
		// A fully drafted task is not a completed task and cannot start its Approach.
		f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "task", ID: first}
		st, err := planningRead(f.d, f.scope)
		if err != nil || st.Phase != "task_drafted" {
			t.Fatal(st.Phase, err)
		}
		f.call(tool.Args{"invoke": "NEXT"})
		f.call(tool.Args{"invoke": "TASK", "title": "second", "objective": "second bounded change"})
		second := draftPipelineTask(t, f)
		a, _ := plan.GetApproach(f.d.Store, aid)
		if a.State != plan.ApproachDraft {
			t.Fatal("task draft started Approach", a)
		}
		f.call(tool.Args{"invoke": "COMPLETE"})
		qualifyPipelineTask(t, f, first)
		qualifyPipelineTask(t, f, second)
		st, err = planningRead(f.d, f.scope)
		if err != nil || st.Phase != "validated" {
			t.Fatal(st.Phase, err)
		}
		a, _ = plan.GetApproach(f.d.Store, aid)
		if a.State != plan.ApproachRunning {
			t.Fatal("count auto-completed approach", a)
		}
		f.call(tool.Args{"invoke": "NEXT_ARTICLE"})
		st, err = planningRead(f.d, f.scope)
		if err != nil {
			t.Fatal(err)
		}
		if approach == 0 {
			if st.Phase != "approach" || st.Article != "second change" {
				t.Fatal(st)
			}
			f.call(tool.Args{"strategy": "second deliverable strategy"})
			f.call(tool.Args{"title": "first", "objective": "first bounded change"})
		} else {
			if st.Phase != "plan_complete" {
				t.Fatal(st.Phase)
			}
			res := f.call(tool.Args{"invoke": "COMPLETE"})
			if res.Workflow == nil || !res.Workflow.Qualified {
				t.Fatal(res)
			}
		}
	}
}

func TestGuidedPlanningClosureGapPreservesQualifiedTask(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	first := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, first)
	f.call(tool.Args{"invoke": "TASK", "title": "remaining gap", "objective": "missing acceptance edge"})
	original, _ := plan.GetTask(f.d.Store, first)
	if !original.Qualified() {
		t.Fatal("gap discarded prior qualification")
	}
	node, _ := plan.GetTask(f.d.Store, f.scope.ID)
	if node.Prompt != "missing acceptance edge" || node.Attempts != 0 {
		t.Fatal(node)
	}
	if !strings.Contains(f.definition().Schema(), "current") {
		t.Fatal("gap did not enter existing Q/A")
	}
}

func TestGuidedPlanningUnambiguousResumeAndStaleClosure(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	task := draftPipelineTask(t, f)
	aid := f.scope.ID
	p, err := plan.ApproachPlan(f.d.Store, aid)
	if err != nil {
		t.Fatal(err)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}
	if def := f.definition(); def.Params[0].Name != "invoke" || def.Params[0].Enum[0] != "NEXT" {
		t.Fatal(def.Schema())
	}
	f.call(tool.Args{"invoke": "NEXT"})
	if f.scope.ID != aid {
		t.Fatal("resume chose wrong approach")
	}
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, task)
	def := f.definition()
	bound, args, err := def.Bind(context.Background(), tool.Args{"invoke": "NEXT_ARTICLE"})
	if err != nil {
		t.Fatal(err)
	}
	// An independent lifecycle change after presentation invalidates the action.
	if _, err = plan.TransitionApproach(f.d.Store, aid, plan.ApproachPaused); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Execute(context.Background(), args); err == nil {
		t.Fatal("stale closure completed changed owner")
	}
	actual, _ := plan.GetApproach(f.d.Store, aid)
	if actual.State != plan.ApproachPaused {
		t.Fatal(actual)
	}
}

func TestGuidedPlanningPausedQualifiedClosureNeedsReadiness(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, id)
	aid := f.scope.ID
	// Models a partial pause/create-gap operation: pause committed, no new task.
	if _, err := plan.TransitionApproach(f.d.Store, aid, plan.ApproachPaused); err != nil {
		t.Fatal(err)
	}
	before := f.judgments
	f.call(tool.Args{"invoke": "COMPLETE"})
	if f.judgments != before+1 {
		t.Fatal("paused closure bypassed readiness")
	}
	st, err := planningRead(f.d, f.scope)
	if err != nil || st.Phase != "validated" {
		t.Fatal(st.Phase, err)
	}
	res := f.call(tool.Args{"invoke": "NEXT_ARTICLE"})
	if !strings.Contains(res.Text, `"effects"`) {
		t.Fatal("closure omitted owner effect receipt", res.Text)
	}
	a, _ := plan.GetApproach(f.d.Store, aid)
	if a.State != plan.ApproachComplete {
		t.Fatal(a)
	}
}

func TestGuidedPlanningReadiedQualifiedGapReturnsToDrafting(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, id)
	aid := f.scope.ID
	if _, err := plan.TransitionApproach(f.d.Store, aid, plan.ApproachPaused); err != nil {
		t.Fatal(err)
	}
	f.call(tool.Args{"invoke": "COMPLETE"})
	f.call(tool.Args{"invoke": "TASK", "title": "newly recognized gap", "objective": "missing edge"})
	a, _ := plan.GetApproach(f.d.Store, aid)
	original, _ := plan.GetTask(f.d.Store, id)
	if a.State != plan.ApproachPaused || !a.NeedsReady || !original.Qualified() {
		t.Fatal(a, original)
	}
}
