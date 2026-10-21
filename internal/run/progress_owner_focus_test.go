package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Real Kuzu/GraphQL/Runner wiring, no model or judge inference. A restored
// assignment is historical evidence, not the selection of a later request.
func TestProgressOwnerFocusSurvivesHistoricalAssignment(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	oldTask := f.scope.ID
	oldApproach, err := plan.TaskApproach(f.d.Store, oldTask)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.ApproachPlan(f.d.Store, oldApproach.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(f.d.Store, p.ID, `{"article_index":1,"article":"second change","strategy":"second deliverable"}`)
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(f.d.Store, a.ID, "next task", "next objective")
	if err != nil {
		t.Fatal(err)
	}
	r := consistencyRunner(t, f, f.scope, "coding")
	r.Continuation.Assignment = &session.Assignment{Store: f.d.Store.Identity(), Workspace: f.d.Workspace, PlanID: p.ID, ApproachID: oldApproach.ID, TaskID: oldTask, Attempt: 1}
	for _, tc := range []struct{ kind, id, step string }{
		{"approach", a.ID, "approach-draft"}, {"task", task.ID, "task-queued"},
		{"plan", p.ID, "plan-active"}, {"task", oldTask, "task-queued"},
	} {
		t.Run(tc.kind+"/"+tc.id, func(t *testing.T) {
			r.Continuation.Workflow = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: tc.kind, ID: tc.id}
			actual, err := r.Assigned.Facts(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			facts := actual.(agent.BookkeepingFacts)
			if facts.Checkpoint.SelectedID != tc.id || facts.Checkpoint.PATStep != tc.step || facts.Checkpoint.Phase != "PREPARE" {
				t.Fatalf("owning focus overwritten by historical assignment: %+v", facts.Checkpoint)
			}
			if facts.Preparation == "" || r.Continuation.Assignment.TaskID != oldTask {
				t.Fatal("projection lost current question or changed assignment")
			}
		})
	}
}

func TestProgressOwnerCoverageProjectionUsesActualQualification(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	taskID := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	a, err := plan.GetApproach(f.d.Store, f.scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.ApproachPlan(f.d.Store, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := consistencyRunner(t, f, &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}, "coding")
	assertProjection := func(stage, approachState, taskState string, approachQualified, taskQualified bool) {
		t.Helper()
		actual, err := r.Assigned.Facts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		facts := actual.(agent.BookkeepingFacts)
		var inventory struct {
			Active []struct {
				ID, Goal, State string
				Approaches      []struct {
					ID, Title, State string
					Qualified        *bool
					Tasks            []struct {
						ID, State string
						Qualified *bool
					}
				}
			}
		}
		if err := json.Unmarshal(facts.Unfinished, &inventory); err != nil {
			t.Fatal(err)
		}
		if len(inventory.Active) != 1 || len(inventory.Active[0].Approaches) != 1 {
			t.Fatalf("wrong bounded inventory: %s", facts.Unfinished)
		}
		shownPlan := inventory.Active[0]
		shownApproach := shownPlan.Approaches[0]
		articles, err := plan.GoalArticles(shownPlan.Goal)
		if err != nil || len(articles) != 2 || articles[1] != "second change" {
			t.Fatalf("authored articles missing: %s %v", shownPlan.Goal, err)
		}
		refs, strategy, err := plan.ApproachCoverage(shownApproach.Title)
		if err != nil || len(refs) != 1 || refs[0].Index != 0 || refs[0].Text != articles[0] || strategy != "repair owner with regression" {
			t.Fatalf("actual coverage/strategy missing: %s %v", shownApproach.Title, err)
		}
		if shownPlan.ID != p.ID || shownPlan.State != plan.PlanActive || shownApproach.ID != a.ID || shownApproach.State != approachState || shownApproach.Qualified == nil || *shownApproach.Qualified != approachQualified || len(shownApproach.Tasks) != 1 || shownApproach.Tasks[0].ID != taskID || shownApproach.Tasks[0].State != taskState || shownApproach.Tasks[0].Qualified == nil || *shownApproach.Tasks[0].Qualified != taskQualified {
			t.Fatalf("state substituted for actual owning qualification: %s", facts.Unfinished)
		}
		if approachQualified && !strings.Contains(facts.Preparation, "Suggested coverage if omitted: article 2") {
			t.Fatalf("next uncovered article missing: %s", facts.Preparation)
		}
		raw, _ := json.Marshal(map[string]any{"stage": stage, "owning_inventory": json.RawMessage(facts.Unfinished), "checkpoint": facts.Checkpoint, "next_action": facts.Preparation})
		t.Logf("actual-owner-projection: %s", raw)
	}
	assertProjection("readied", plan.ApproachReady, plan.TaskQueued, false, false)
	qualifyPipelineTask(t, f, taskID)
	assertProjection("task-qualified-parent-open", plan.ApproachRunning, plan.TaskDone, false, true)
	f.call(tool.Args{"invoke": "NEXT_ARTICLE"})
	assertProjection("approach-closed-article2-uncovered", plan.ApproachComplete, plan.TaskDone, true, true)
}

func TestProgressOwnerKnowledgeUsesCurrentTask(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	oldTask := f.scope.ID
	a, err := plan.TaskApproach(f.d.Store, oldTask)
	if err != nil {
		t.Fatal(err)
	}
	next, err := plan.CreateTask(f.d.Store, a.ID, "new task", "current objective")
	if err != nil {
		t.Fatal(err)
	}
	r := consistencyRunner(t, f, f.scope, "coding")
	r.SessionID = "current-session"
	r.Continuation.Assignment = &session.Assignment{Store: f.d.Store.Identity(), Workspace: f.d.Workspace, TaskID: oldTask, Attempt: 1}
	for _, note := range []struct{ key, task, file, ref string }{
		{"old-task-only", oldTask, "", "old-session"},
		{"new-task-only", next.ID, "", "old-session"},
		{"same-session", oldTask, "", r.SessionID},
		{"same-file", oldTask, "source.go", "old-session"},
	} {
		if _, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, note.ref, note.key, note.key+" fact", "", note.task, note.file); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(f.d.Workspace, "source.go")
	if err := os.WriteFile(path, []byte("package source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read, ok := r.Registry.Lookup("read")
	if !ok {
		t.Fatal("real read owner missing")
	}
	args := tool.Args{"path": path, "offset": int64(1), "limit": int64(10)}
	result, err := read.Execute(t.Context(), args)
	if err != nil || result.Failed {
		t.Fatalf("real read failed: %+v %v", result, err)
	}
	r.Assigned.Observe(read, args, result, err)
	for _, focus := range []struct{ kind, id, wanted string }{
		{"task", next.ID, "new-task-only"}, {"approach", a.ID, ""}, {"task", oldTask, "old-task-only"},
	} {
		t.Run(focus.kind+"/"+focus.id, func(t *testing.T) {
			r.Continuation.Workflow = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: focus.kind, ID: focus.id}
			actual, err := r.Assigned.Facts(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			keys := map[string]bool{}
			for _, entry := range actual.(agent.BookkeepingFacts).Knowledge {
				keys[entry["key"]] = true
			}
			if !keys["same-session"] || !keys["same-file"] || keys["old-task-only"] != (focus.wanted == "old-task-only") || keys["new-task-only"] != (focus.wanted == "new-task-only") {
				t.Fatalf("knowledge used historical task or lost actual session/file links: %+v", keys)
			}
		})
	}
}
