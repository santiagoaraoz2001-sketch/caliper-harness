package prompts

import (
	"strings"
	"testing"
)

// These are emitted instruction compatibility controls, not model-behavior tests.
func TestR3ReviewAndBlockedCheckContracts(t *testing.T) {
	pc := Builtin()
	for _, key := range []string{"templates.review", "agent.reviewer_role"} {
		text := strings.Join(strings.Fields(pc.texts[key]), " ")
		for _, want := range []string{"this review's current", "checks add", "verdict"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", key, want)
			}
		}
	}
	for _, key := range []string{"system.coding.requirements", "system.coding.direct"} {
		text := strings.Join(strings.Fields(pc.texts[key]), " ")
		if !strings.Contains(text, "declared checker is conclusively unavailable") || !strings.Contains(text, "within this task") {
			t.Errorf("%s omits bounded missing-checker response", key)
		}
	}
	missing := pc.Text("verification.current_check_missing")
	if !strings.Contains(missing, "Do not substitute") || !strings.Contains(missing, "No pass") {
		t.Fatal("missing-check contract weakened")
	}
}
