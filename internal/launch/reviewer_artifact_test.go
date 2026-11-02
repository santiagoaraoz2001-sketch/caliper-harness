package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// A frozen check that writes artifacts (screenshots, coverage) must not
// make its own review impossible: the reviewer's check runs, the
// workspace is restored to its pre-check contents, and the review can
// still reach a verdict (candidate38 Task 3 died exactly here).
func TestReviewerCheckArtifactsRestoredNotViolation(t *testing.T) {
	s, parent, approach, task, d := projectWorkflowFixture(t)
	// Production registries bind bash to the workspace; mirror that so
	// the frozen check actually writes inside the reviewed project.
	d.NewRegistry = func() *tool.Registry {
		reg := tool.NewRegistry()
		reg.Register(bash.NewAt(config.Defaults().Tools.Bash, d.Workspace))
		return reg
	}
	check := "printf shot > artifact.txt && printf y >> keep.txt && printf CHECKOK"
	if err := os.WriteFile(filepath.Join(d.Workspace, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.SetTaskBlock(s, task.ID, "now", "after", "mech", "artifacts do not block review", "TestArtifacts", "inspect", check, check); err != nil {
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
	step := 0
	sawCheckOutput := false
	d.NewModel = func() agent.Model {
		return roleHandoffModel(func(messages []llm.Message) (string, error) {
			step++
			if step == 1 {
				return "```tool\ntool: check\ncmd: " + check + "\n```", nil
			}
			for _, m := range messages {
				if strings.Contains(m.Content, "CHECKOK") {
					sawCheckOutput = true
				}
			}
			return "PASS: check episode passed; artifacts were written by the frozen check itself", nil
		})
	}
	r := Result{TaskID: task.ID, State: task.State, Attempts: task.Attempts, AttemptLog: task.AttemptLog, brief: task}
	c := &session.CodingState{Workspace: d.Workspace, TaskID: task.ID, Command: task.CheckCommand, AcceptanceCommand: task.AcceptanceCommand}
	_, _, status, err := runReviewer(t.Context(), d, "Review artifact-writing check", 32768, &r, task, c)
	if !sawCheckOutput {
		t.Fatalf("reviewer check never executed (steps=%d status=%+v err=%v)", step, status, err)
	}
	if err != nil || status.Status != outcome.Completed {
		t.Fatalf("review: status=%+v err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Join(d.Workspace, "artifact.txt")); !os.IsNotExist(err) {
		t.Fatalf("check artifact not restored away: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(d.Workspace, "keep.txt"))
	if err != nil || string(b) != "keep" {
		t.Fatalf("pre-existing file not restored: %q %v", b, err)
	}
}
