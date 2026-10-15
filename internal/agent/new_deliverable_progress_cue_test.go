package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// Exercise the owning current-claim projection that emitted the contradictory
// live advice. Authored new work is not proof of a pre-existing defect.
func TestNewDeliverableCurrentClaimProgressCue(t *testing.T) {
	r := &Runner{
		Prompts:       prompts.Builtin(),
		Assigned:      &AssignedOps{Store: "actual-store"},
		Workspace:     "/actual-project",
		assignedClaim: true,
		Outcome:       outcome.Outcome{Status: outcome.Unknown},
		Continuation: session.Continuation{
			Assignment: &session.Assignment{Store: "actual-store", Workspace: "/actual-project", PlanID: "actual-plan", ApproachID: "actual-approach", TaskID: "actual-task", Attempt: 1},
			Coding:     &session.CodingState{TaskID: "actual-task", Workspace: "/actual-project", Attempt: 1, Command: "npm test", AcceptanceCommand: "node --test test/snake.test.js"},
		},
	}
	facts := BookkeepingFacts{Assignment: map[string]string{"prompt": "Build a new browser Snake game", "current": "No game exists", "target": "A new tested Snake engine"}}
	checkpoint := r.currentProgress(facts)
	if checkpoint.Phase != "EXECUTE" || checkpoint.PATStep != "claimed" || checkpoint.SelectedID != "actual-task" {
		t.Fatalf("control did not emit the actual current-claim cue: %+v", checkpoint)
	}
	// Execution procedure belongs to the shared coding instructions; the task
	// header now identifies the frozen owner rather than repeating that procedure.
	role := r.Prompts.Format("system.task.execute", "actual-task", 1) + r.Prompts.Text("system.coding.requirements")
	if !strings.Contains(role, "For a reported defect") || !strings.Contains(role, "For a new deliverable") {
		t.Fatal("control lacks the existing conditional claimed-task role", role)
	}
	for _, conditional := range []string{"For a reported defect", "For a new deliverable"} {
		if !strings.Contains(checkpoint.NextAction, conditional) {
			t.Errorf("emitted advice contradicts the existing role for new work: missing %q in %q", conditional, checkpoint.NextAction)
		}
	}
	if !strings.Contains(checkpoint.NextAction, "check") || !strings.Contains(checkpoint.NextAction, "independent review") {
		t.Error("current check/review obligation missing from execution cue", checkpoint.NextAction)
	}
}
