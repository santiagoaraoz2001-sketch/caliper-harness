package launch

import (
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	graphTool "specific-harness/internal/tools/graph"
	"strings"
	"testing"
)

func TestReviewerReadsGraphIdentityButCannotWrite(t *testing.T) {
	s, parent, approach, task, d := projectWorkflowFixture(t)
	// A reviewer owns an admitted attempt, never a queued task. Keep the role
	// read/mutation test on the same real admission route as production review.
	if _, err := plan.SetTaskBlock(s, task.ID, "now", "after", "mech", "identity is readable", "TestIdentity", "inspect graph identity", "printf CHECK", "printf ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureApproachTransition(s, approach.ID, plan.ApproachReady); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionPlan(s, parent.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := plan.ContractSnapshot(plan.WithProject(t.Context(), d.Workspace), s, "task", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = plan.ClaimTaskContractContext(plan.WithProject(t.Context(), d.Workspace), s, task.ID, snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.PutKnowledgeReferences(t.Context(), s, d.Workspace, "author", "project/design/identity", "Copper UI identity", "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	step := 0
	d.NewModel = func() agent.Model {
		return roleHandoffModel(func(messages []llm.Message) (string, error) {
			step++
			all := ""
			for _, m := range messages {
				all += m.Content + "\n"
			}
			switch step {
			case 1:
				return "```tool\ntool: graphql\nquery: {knowledge(key:\"project/design/identity\"){body}}\n```", nil
			case 2:
				if !strings.Contains(all, "Copper UI identity") {
					t.Error("reviewer did not receive graph identity")
				}
				return "```tool\ntool: graphql\nquery: mutation{appendKnowledge(key:\"review-write\",body:\"no\",expectedPreviousId:\"\"){id}}\n```", nil
			default:
				if !strings.Contains(all, "unavailable in this role") {
					t.Error("reviewer mutation was not refused")
				}
				return "PASS: identity retrieved; mutation correctly refused", nil
			}
		})
	}
	d.NewKnowledge = func(ref string, readOnly bool) (tool.Definition, error) {
		if ref != task.ID || !readOnly {
			t.Errorf("wrong owner/role: %s %v", ref, readOnly)
		}
		return graphTool.New(api.New(s, "test", t.TempDir()).WithAllowedFields("knowledge"), config.Defaults().Tools.GraphQL, d.Workspace, ref, s.Identity(), d.Prompts)
	}
	r := Result{TaskID: task.ID, State: task.State, Attempts: task.Attempts, AttemptLog: task.AttemptLog, brief: task}
	c := &session.CodingState{Workspace: d.Workspace, TaskID: task.ID, Command: task.CheckCommand}
	_, _, status, err := runReviewer(t.Context(), d, "Review graph identity", 32768, &r, task, c)
	if err != nil || status.Status != outcome.Completed || step != 3 {
		t.Fatalf("review: steps=%d status=%+v err=%v", step, status, err)
	}
	rows, err := memory.Knowledge(t.Context(), s, d.Workspace, "review-write", "", 1, 10000, true)
	if err != nil || len(rows) != 0 {
		t.Fatalf("review mutated: %v %v", rows, err)
	}
}
