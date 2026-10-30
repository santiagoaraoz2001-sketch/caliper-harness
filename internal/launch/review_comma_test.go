package launch

import (
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

// Distilled from the actual repeated opener in reviewer 20261028-232651-ed4c.
// This tests verdict transport, not the unsupported extra-file finding or
// rendered appearance; full raw evidence stays outside the source tree.
const capturedCommaOpener = "PASS, BEHAVIOR_MISMATCH for the missing task-level test file, and EVIDENCE_MISSING for rendered visual appearance."

func TestReviewCommaVerdictBoundaries(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{capturedCommaOpener + "\n\nThe frozen behavioral assertions passed. A screenshot was unavailable.", "PASS"},
		{"PASS, observations follow", "PASS"}, {"FAIL, behavior is wrong", "FAIL"}, {"UNVERIFIED, rendered evidence missing", "UNVERIFIED"},
		{"**PASS**, observations follow", "PASS"}, {"**PASS,** observations follow", "PASS"}, {"_PASS_, observations follow", "PASS"},
		{"Observed source.\n\nPASS, supported conclusion", "PASS"}, {"## PASS, supported conclusion", "PASS"},
		{"PASS, FAIL", ""}, {"PASS, or FAIL", ""}, {"PASS, — UNVERIFIED", ""}, {"**PASS**, FAIL", ""},
		{"PASS,FAIL", ""}, {"PASS,, observations", ""}, {"PASSABLE, observations", ""}, {"NOTPASS, observations", ""},
		{"\"PASS,\" observed", ""}, {"`PASS,` observed", ""}, {"> PASS, observed", ""}, {"    PASS, observed", ""},
		{"```text\nPASS, observed\n```", ""}, {"Example:\n\nPASS, observed", ""}, {"## Examples\n\nPASS, observed", ""},
		{"PASS, supported\n\nFAIL: actual conflicting conclusion", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := reviewToken(tc.text); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestCapturedCommaReviewNeedsNoFormatGeneration(t *testing.T) {
	content := capturedCommaOpener + "\n\nBehavioral acceptance passed; rendered visual appearance remains unverified."
	workers, reviews := 0, 0
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		if strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
			reviews++
			if reviews == 1 {
				return content, nil
			}
			return "PASS: controlled format-only fallback", nil
		}
		workers++
		return "controlled component artifact", nil
	})
	d, aid, _, _ := roleHandoffFixture(t, 1, model)
	results, err := Launch(t.Context(), d, aid)
	if err != nil || len(results) != 1 || !results[0].Qualified() || results[0].Review != content || workers != 1 || reviews != 1 {
		t.Fatalf("format-only retry or owning checks bypassed: workers=%d reviews=%d result=%+v err=%v", workers, reviews, results, err)
	}
}
