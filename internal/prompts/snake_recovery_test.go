package prompts

import (
	"strings"
	"testing"
)

func TestExistingWorkGuidancePreservesVerification(t *testing.T) {
	for _, key := range []string{"system.coding.requirements", "system.coding.direct", "system.task.handoff", "system.task.prepare", "help.plan", "planning.proof", "help.reference.pat", "help.reference.workers", "progress.execute", "preparation.fresh_typed"} {
		if !strings.Contains(Builtin().Text(key), "already implemented") {
			t.Errorf("%s misses existing-work branch", key)
		}
	}
	for _, want := range []string{"host receipts mark them current for this task and attempt", "Source changes invalidate prior passes", "independent review"} {
		if !strings.Contains(Builtin().Text("system.task.handoff"), want) {
			t.Errorf("handoff lost %q", want)
		}
	}
	for _, key := range []string{"planning.proof", "planning.answer.acceptance_command"} {
		if !strings.Contains(Builtin().Text(key), "nonzero") {
			t.Errorf("%s does not distinguish assertions from printed booleans", key)
		}
	}
	if !strings.Contains(Builtin().Text("planning.proof"), "same suite") {
		t.Fatal("unnecessary separate-command restriction")
	}
}
