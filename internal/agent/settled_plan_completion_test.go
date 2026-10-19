package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Replays the captured main-loop shape: qualified task, final report, another
// normal final. A Task receipt cannot stand in for the parent Plan receipt.
// Owners are controlled here; the production Run, claim, handoff and provenance
// paths are exercised without model inference or changes to the live fixture.
func TestSettledPlanCompletionContinuesThroughParentOwners(t *testing.T) {
	t.Run("parent-closure", func(t *testing.T) { runSettledPlanCompletion(t, 10) })
	t.Run("qualification-at-last-step", func(t *testing.T) { runSettledPlanCompletion(t, 2) })
}

func runSettledPlanCompletion(t *testing.T, stepBudget int) {
	pc := prompts.Builtin()
	workspace := t.TempDir()
	stage, settlements, completionReads := 0, 0, 0
	questions := []string{"task execution pending", "Qualified Task: invoke NEXT to inspect its Approach.", "Qualified Approach: invoke NEXT_ARTICLE to close it.", "All Plan articles covered: invoke COMPLETE to close the Plan.", "Plan complete."}
	model := &scriptModel{replies: []string{
		"```tool\ntool: task_start\ntask: current-task\n```",
		"Task complete; request independent review.",
		"Both tasks validated. Work complete.", // Captured failure: no parent call.
		"```tool\ntool: plan\ninvoke: NEXT\n```",
		"```tool\ntool: plan\ninvoke: NEXT_ARTICLE\n```",
		"```tool\ntool: plan\ninvoke: COMPLETE\n```",
		"Plan, Approach and Tasks are complete.",
	}}
	r := &Runner{Prompts: pc, Model: model, Registry: tool.NewRegistry(pc), Session: &recordSink{}, Workspace: workspace, MaxSteps: stepBudget,
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "current-task"}}}
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: coherent, Confidence: .9}, nil
	}
	r.Assigned = &AssignedOps{Reset: func() {}, Admit: func(context.Context) error { return nil },
		Start: func(context.Context, string) (*session.Assignment, string, error) {
			return &session.Assignment{Store: "owner", Workspace: workspace, PlanID: "current-plan", ApproachID: "current-approach", TaskID: "current-task", Attempt: 1}, "", nil
		},
		Facts: func(context.Context) (any, error) {
			return BookkeepingFacts{Preparation: questions[stage], Checkpoint: ProgressCheckpoint{Phase: "HANDOFF"}}, nil
		},
		Settle: func(_ context.Context, status outcome.Outcome, _, _, _ string, _ []llm.Message, _ *session.CodingState) (TaskSettlement, error) {
			settlements++
			if settlements == 1 && status.Status != outcome.Completed {
				t.Fatalf("qualified task was resettled as %+v during parent closure", status)
			}
			if stage == 0 {
				stage = 1
			}
			return TaskSettlement{TaskID: "current-task", Attempt: 1, State: "done", Qualified: true, Check: outcome.Passed, Verification: outcome.Passed}, nil
		}}
	r.WorkflowView = func(scope *session.WorkflowScope) (*tool.Registry, string, error) {
		reg := tool.NewRegistry(pc)
		reg.Register(r.TaskStartDefinition())
		def := tool.Definition{Name: "plan", Purpose: questions[stage], Params: []tool.Param{{Name: "invoke", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
			want := map[int]string{1: "NEXT", 2: "NEXT_ARTICLE", 3: "COMPLETE"}[stage]
			if a["invoke"] != want || want == "" {
				return tool.Result{}, fmt.Errorf("unexpected transition at stage %d: %v", stage, a)
			}
			stage++
			result := tool.Result{Text: questions[stage]}
			if stage == 4 {
				result.Workflow = &outcome.Report{Ref: "current-plan", Qualified: true, Outcome: outcome.Outcome{Status: outcome.Completed}}
			}
			return result, nil
		}}
		// Match production: only actual final Plan completion publishes the
		// qualified workflow receipt; NEXT is navigation, not verification.
		if stage == 3 {
			def.WorkflowRefArg = "invoke"
			def.ResolveWorkflowRef = func(context.Context, tool.Args) (string, error) { return "current-plan", nil }
		}
		reg.Register(def)
		return reg, "Current selected PAT", nil
	}
	r.PlanningCompletion = func(*session.WorkflowScope) (*outcome.Report, error) {
		completionReads++
		return &outcome.Report{Ref: "current-plan", Qualified: stage == 4}, nil
	}
	final, _, err := r.Run(t.Context(), "Complete the selected two-fix Plan with real review and parent handoff.")
	if stepBudget == 2 {
		if err != nil || r.Outcome.Status != outcome.Stopped || stage != 1 || settlements != 1 || r.settlement == nil || !r.settlement.Qualified {
			t.Fatalf("last-step qualification fabricated parent completion: outcome=%+v stage=%d settlements=%d err=%v", r.Outcome, stage, settlements, err)
		}
		if !strings.Contains(r.Outcome.Reason, "parent handoff pending") || !strings.Contains(r.Evidence, "Run stopped:") || !strings.Contains(r.Evidence, "Qualified Task evidence retained") {
			t.Fatalf("qualified Task evidence or pending handoff lost: outcome=%+v evidence=%s", r.Outcome, r.Evidence)
		}
		if completionReads != 0 || len(model.seen) != 2 {
			t.Fatalf("exhausted run continued: ownerReads=%d requests=%d", completionReads, len(model.seen))
		}
		return
	}
	if err != nil || r.Outcome.Status != outcome.Completed || stage != 4 || final != "Plan, Approach and Tasks are complete." {
		t.Fatalf("parent completion bypass: outcome=%+v stage=%d final=%q calls=%d settlements=%d ownerReads=%d err=%v", r.Outcome, stage, final, len(model.seen), settlements, completionReads, err)
	}
	if completionReads == 0 {
		t.Fatal("completed without current owning Plan readback")
	}
	for _, index := range []int{2, 3} {
		if len(model.seen) <= index {
			t.Fatalf("no continuation request %d after qualified settlement", index)
		}
		shown := ""
		for _, message := range model.seen[index] {
			shown += message.Content + "\n"
		}
		if !strings.Contains(shown, questions[1]) {
			t.Fatalf("continuation request %d lacks exact current NEXT question", index)
		}
	}
}

func TestSettledPlanQuestionSurvivesRetainedAssignment(t *testing.T) {
	const question = "CAPTURED NEXT: Task qualified; inspect its Approach with NEXT."
	reg := tool.NewRegistry(prompts.Builtin())
	reg.Register(tool.Definition{Name: "plan", Purpose: question})
	r := &Runner{Prompts: prompts.Builtin(), Registry: reg, Assigned: &AssignedOps{}, assignedClaim: true,
		settlement:   &TaskSettlement{TaskID: "task", Attempt: 1, State: "done", Qualified: true},
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "task"}, Assignment: &session.Assignment{Store: "owner", TaskID: "task", Attempt: 1}}}
	r.begin("continue selected work")
	if err := r.refreshWorkflow(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range r.Continuation.Entries {
		found = found || entry.Kind == "guidance" && strings.Contains(entry.Message.Content, question)
	}
	if !found {
		t.Fatal("retained settled assignment suppressed current planning question")
	}
}

func TestSettledPlanUnqualifiedNeverClosesParents(t *testing.T) {
	for _, name := range []string{"review-missing", "review-rejected", "managed-stop"} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			pc := prompts.Builtin()
			maxSteps := 6
			if name == "managed-stop" {
				maxSteps = 1
			}
			m := &scriptModel{replies: []string{"```tool\ntool: task_start\ntask: current-task\n```", "Please review.", "I claim it is complete."}}
			r := &Runner{Prompts: pc, Model: m, Registry: tool.NewRegistry(pc), Session: &recordSink{}, Workspace: workspace, MaxSteps: maxSteps,
				Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "current-task"}}}
			r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				return decision.Verdict{Option: coherent, Confidence: .9}, nil
			}
			settlements := 0
			r.Assigned = &AssignedOps{Reset: func() {}, Admit: func(context.Context) error { return nil },
				Start: func(context.Context, string) (*session.Assignment, string, error) {
					return &session.Assignment{Store: "owner", Workspace: workspace, PlanID: "current-plan", ApproachID: "current-approach", TaskID: "current-task", Attempt: 1}, "", nil
				},
				Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil },
				Settle: func(_ context.Context, status outcome.Outcome, _, _, _ string, _ []llm.Message, _ *session.CodingState) (TaskSettlement, error) {
					settlements++
					if name == "managed-stop" && status.Status != outcome.Stopped {
						t.Fatalf("managed stop lost: %+v", status)
					}
					verification := outcome.Unverified
					if name == "review-rejected" {
						verification = outcome.Rejected
					}
					return TaskSettlement{TaskID: "current-task", Attempt: 1, State: "paused", Verification: verification}, nil
				}}
			r.Registry.Register(r.TaskStartDefinition())
			r.PlanningCompletion = func(*session.WorkflowScope) (*outcome.Report, error) {
				t.Fatal("unqualified claim was allowed to assess parent closure")
				return nil, nil
			}
			_, _, err := r.Run(t.Context(), "Complete selected work after real verification.")
			if err != nil || r.Outcome.Status != outcome.Stopped || settlements == 0 {
				t.Fatalf("unqualified outcome=%+v settlements=%d error=%v", r.Outcome, settlements, err)
			}
			if len(m.seen) > 3 {
				t.Fatal("unqualified claim silently resumed or repeated review")
			}
		})
	}
}

func TestSettledPlanScopeUsesOnlyCurrentQualifiedAssignment(t *testing.T) {
	r := &Runner{Assigned: &AssignedOps{}, assignedClaim: true, Workspace: "/workspace",
		settlement:   &TaskSettlement{TaskID: "task", Attempt: 1, Qualified: true},
		Continuation: session.Continuation{Assignment: &session.Assignment{Store: "owner", Workspace: "/workspace", TaskID: "task", Attempt: 1}}}
	scope := r.PlanningScope()
	if scope == nil || scope.Kind != "task" || scope.ID != "task" || scope.Store != "owner" || r.Continuation.Workflow != nil {
		t.Fatalf("scope projection changed authority: %+v %+v", scope, r.Continuation.Workflow)
	}
	seen := false
	r.Prompts = prompts.Builtin()
	r.WorkflowView = func(s *session.WorkflowScope) (*tool.Registry, string, error) {
		seen = s.ID == "task" && s.Store == "owner"
		reg := tool.NewRegistry(r.Prompts)
		reg.Register(tool.Definition{Name: "plan", Purpose: "Qualified Task: NEXT"})
		return reg, "", nil
	}
	if err := r.refreshWorkflow(); err != nil || !seen || r.Continuation.Workflow != nil {
		t.Fatalf("derived view missing or focus invented: %v %t %+v", err, seen, r.Continuation.Workflow)
	}
	r.assignedClaim = false
	if r.PlanningScope() != nil {
		t.Fatal("restored assignment acquired focus")
	}
	r.assignedClaim = true
	r.settlement.Attempt = 2
	if r.PlanningScope() != nil {
		t.Fatal("mismatched settlement acquired focus")
	}
	r.settlement.Attempt = 1
	r.settlement.Qualified = false
	if r.PlanningScope() != nil {
		t.Fatal("unqualified settlement acquired focus")
	}
	explicit := &session.WorkflowScope{Store: "owner", Kind: "plan", ID: "explicit"}
	r.Continuation.Workflow = explicit
	if r.PlanningScope() != explicit {
		t.Fatal("explicit selection replaced")
	}
}
