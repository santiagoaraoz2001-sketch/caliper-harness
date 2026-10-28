package prompts

import (
	"strings"
	"testing"
)

func TestCompletionGuidanceAgreesAcrossRolesAndReferences(t *testing.T) {
	pc := Builtin()
	for _, key := range []string{"system.task.handoff", "planning.running", "verification.paired_passed", "progress.validate", "help.plan", "help.reference.pat", "help.reference.workers"} {
		text := strings.Join(strings.Fields(pc.texts[key]), " ")
		for _, want := range []string{"normal final answer", "no tool call", "independent review"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s omits %q", key, want)
			}
		}
	}
	// launch composes the frozen worker brief with the same handoff used by
	// main-task execution. Requiring a second copy in the brief caused bloat.
	worker := strings.Join(strings.Fields(pc.texts["templates.subagent"]+"\n"+pc.Text("system.task.handoff")+"\n"+pc.Text("system.coding.requirements")), " ")
	for _, want := range []string{"normal final answer", "no tool call", "independent review"} {
		if strings.Count(worker, want) != 1 {
			t.Errorf("composed worker needs one completion instruction %q", want)
		}
	}
	for _, key := range []string{"system.text.instructions", "system.task.prepare"} {
		if strings.Contains(strings.ToLower(pc.Text(key)), "bash") {
			t.Errorf("%s teaches a shell call unavailable during preparation", key)
		}
	}
	for _, key := range []string{"tools.workflow_block.parameters.acceptance_command", "tools.workflow_block.parameters.check_command"} {
		if text := pc.Text(key); !strings.Contains(text, "check") || strings.Contains(text, "Bash") {
			t.Errorf("%s routes verification to shell instead of check", key)
		}
	}
	if strings.Contains(pc.Text("planning.saved"), "answer only") || strings.Contains(pc.Text("planning.stopped"), "running") {
		t.Fatal("generic navigation overrides current completion state")
	}
}
