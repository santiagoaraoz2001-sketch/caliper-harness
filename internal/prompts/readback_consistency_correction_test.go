package prompts

import (
	"strings"
	"testing"
)

func TestCatalogRecoveryDoesNotRequireRedundantReadback(t *testing.T) {
	for key, text := range Builtin().texts {
		lower := strings.ToLower(text)
		for _, instruction := range []string{"require owning readback", "require readback", "readback before", "read back before", "after owning readback", "owning readback required", "read back owning state"} {
			if strings.Contains(lower, instruction) {
				t.Errorf("%s imposes redundant model query: %s", key, instruction)
			}
		}
	}
}

func TestCatalogCurrentPlanningDoesNotDemandUnavailableActions(t *testing.T) {
	for _, key := range []string{"system.task.prepare", "system.workflow.focused", "continuation.no_claim_final", "preparation.guided_next"} {
		text := Builtin().Text(key)
		for _, laterAction := range []string{"task_start", "workflow_ready", "workflow_approach", "workflow_status"} {
			if strings.Contains(text, laterAction) {
				t.Errorf("%s demands a later/unavailable action %s instead of the current question", key, laterAction)
			}
		}
		// Describing the later advertised start is necessary to distinguish it
		// from method drafting; it must not demand that unavailable action now.
		if strings.Contains(text, "EXECUTE") && !strings.Contains(text, "advertised") {
			t.Errorf("%s describes start without the current-schema condition", key)
		}
	}
}

func TestCatalogSettledReportDoesNotDemandStatusQuery(t *testing.T) {
	for _, key := range []string{"system.workflow.hierarchy", "system.workflow.scoped_launch", "continuation.workflow.ready_next", "continuation.workflow.scoped_ready_next", "tools.launch.purpose"} {
		text := strings.ToLower(strings.Join(strings.Fields(Builtin().texts[key]), " "))
		for _, compulsory := range []string{"then workflow_status", "read workflow_status afterward"} {
			if strings.Contains(text, compulsory) {
				t.Errorf("%s requires another query after a settled report: %s", key, compulsory)
			}
		}
	}
}
