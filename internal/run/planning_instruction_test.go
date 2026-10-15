package run

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// Draft-method and start instructions must describe their actual owning effects.
// Persistence/selection are real; the fixture's task_start refuses without a main
// session, so no execution or qualification is fabricated by this control.
func TestGuidedDraftMethodAndStartInstructionsMatchEffects(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := f.scope.ID
	f.call(tool.Args{"current": "observed defect", "target": "correct requested behavior"})
	f.call(tool.Args{"acceptance": "distinguishing assertions pass", "tests": "planned regression and preserved-behavior tests", "acceptance_command": "test 1 -eq 1"})
	f.call(tool.Args{"objective": "reproduce, repair and verify the requested behavior"})
	draft := f.definition()
	if len(draft.Params) != 1 || draft.Params[0].Name != "execution" {
		t.Fatal("draft method advertises a start action", draft.Schema())
	}
	f.call(tool.Args{"execution": "add and run the regression on original source, repair the behavior, run both current checks"})
	task, err := plan.GetTask(f.d.Store, id)
	if err != nil || task.State != plan.TaskQueued || task.Attempts != 0 {
		t.Fatalf("method answer executed or claimed work: %+v %v", task, err)
	}
	if !strings.Contains(draft.Purpose, "DRAFT METHOD") || !strings.Contains(draft.Purpose, "does not run") {
		t.Errorf("draft method instruction misstates its effect: %s", draft.Purpose)
	}
	role := f.d.Prompts.Text("system.task.prepare")
	if !strings.Contains(role, "answer only plan's current question") || !strings.Contains(role, "pre-claim effects are ordinary project") || !strings.Contains(role, "An actual claim binds the main model") || strings.Contains(role, `plan(invoke="EXECUTE")`) || strings.Contains(role, "execution question is advertised, it claims") {
		t.Errorf("global role confuses the current draft, ordinary coding and actual Task claim: %s", role)
	}
	f.call(tool.Args{"validation": "regression fails before repair and passes after; project suite passes"})
	f.call(tool.Args{"more": false})
	f.call(tool.Args{"verify": "inspect actual requested behavior, regression and current paired checks independently"})
	f.call(tool.Args{"invoke": "COMPLETE"})
	start := f.definition()
	if len(start.Params) != 1 || start.Params[0].Name != "invoke" || len(start.Params[0].Enum) != 1 || start.Params[0].Enum[0] != "EXECUTE" {
		t.Fatal("ready schema does not advertise actual start", start.Schema())
	}
	if !strings.Contains(start.Purpose, `plan(invoke="EXECUTE")`) || !strings.Contains(start.Purpose, "main model") {
		t.Errorf("start instruction does not match the bound main route: %s", start.Purpose)
	}
	bound, args, err := start.Bind(context.Background(), tool.Args{"invoke": "EXECUTE"})
	if err != nil || bound.Name != "task_start" || args["task"] != id {
		t.Fatalf("advertised start bound a different route: %+v %+v %v", bound, args, err)
	}
	currentHelp := f.d.Prompts.Text("help.plan")
	if !strings.Contains(currentHelp, "current question and arguments shown above") || !strings.Contains(currentHelp, "real Task claim") || !strings.Contains(currentHelp, "reference.pat") || strings.Contains(currentHelp, "invoke=") || strings.Contains(currentHelp, "more=") {
		t.Errorf("current plan help competes with future calls or loses its claim/reference boundary: %s", currentHelp)
	}
	for _, topic := range []string{"help.reference.pat", "help.reference.workers"} {
		text := f.d.Prompts.Text(topic)
		if !strings.Contains(text, `plan(invoke="EXECUTE")`) || !strings.Contains(text, "main model") || strings.Contains(text, "EXECUTE dispatches") || strings.Contains(text, "EXECUTE binds the existing worker launcher") || strings.Contains(text, "does not substitute a main-task claim") {
			t.Errorf("%s conflicts with the actual guided claim: %s", topic, text)
		}
	}
}
