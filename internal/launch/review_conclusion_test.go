package launch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func capturedConclusionReviews(t *testing.T) []struct{ Source, Content string } {
	t.Helper()
	raw, err := os.ReadFile("testdata/reviewer-conclusion-verdicts.json")
	if err != nil {
		t.Fatal(err)
	}
	var captures []struct{ Source, Content string }
	if err = json.Unmarshal(raw, &captures); err != nil || len(captures) != 2 {
		t.Fatalf("captured verdict fixture: %d %v", len(captures), err)
	}
	return captures
}

func TestReviewCapturedConclusions(t *testing.T) {
	for _, c := range capturedConclusionReviews(t) {
		t.Run(c.Source, func(t *testing.T) {
			if got := reviewToken(c.Content); got != "PASS" {
				t.Fatalf("explicit captured conclusion became %q", got)
			}
		})
	}
}

func TestReviewConclusionBoundaries(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Evidence first\n## PASS", ""},
		{"Independent review:\n\n## PASS — Task behavior\n\nObserved source and checks.", "PASS"},
		{"Observed defect.\n\nFAIL: acceptance does not hold.", "FAIL"},
		{"Checks absent.\n\n**UNVERIFIED** — missing receipt.", "UNVERIFIED"},
		{"Evidence first\n\nPASS", ""},
		{"## PASS ##\n\nEvidence.", "PASS"},
		{"## PASS — observed\n\nPASS: same conclusion", "PASS"},
		{"## FAIL\n\nPASS requires fixing the observed defect.", "FAIL"},
		{"PASS: observed\n\nFAIL is the value tested by this case.", "PASS"},
		{"Report\n\nPASS / FAIL", ""},
		{"Report\n\n## PASS or FAIL", ""},
		{"PASS\n\nFAIL", ""},
		{"PASS: checks passed\n\nFAIL: behavior is wrong", ""},
		{"PASS\nFAIL", ""},
		{"## PASS — observed\n\nUNVERIFIED: check missing", ""},
		{"Evidence\n\nPASSABLE", ""},
		{"Evidence\n\nThe test says PASS.", ""},
		{"Evidence\n\n`PASS`", ""},
		{"Evidence\n\n\"PASS\"", ""},
		{"Evidence\n\n> ## PASS", ""},
		{"Evidence\n\n    PASS", ""},
		{"Evidence\n\n\tPASS", ""},
		{"Evidence\n\n```text\n\nPASS\n```", ""},
		{"Evidence\n\n~~~~markdown\n## PASS\n~~~\n\nPASS\n~~~~", ""},
		{"Evidence\n\n```text\nFAIL\n```\n\nPASS: authored conclusion", "PASS"},
		{"Example:\n\nPASS", ""},
		{"For example:\n\n## PASS", ""},
		{"## Examples\n\nPASS\n\nFAIL", ""},
		{"## Example verdict\n\nPASS\n\n## Findings\n\nFAIL: actual issue", ""},
		{"## Example output for this task\n\nPASS", ""},
		{"## Example output for this task\n\n### PASS — sample", ""},
		{"Example: requested output\n\nPASS — sample", ""},
		{"Expected output:\n\nPASS", ""},
		{"Expected output:\n\nPASS — sample", ""},
		{"## Evidence\n\nObserved checks.\n\n## PASS — Final review", "PASS"},
		{"PASS: actual conclusion\n\nExample:\n\nFAIL", "PASS"},
		{"Evidence\n\n<!--\n\n## PASS\n\n-->", ""},
		{"Evidence\n\n**PASS*", ""},
		{"Evidence\n\n## PASS:FAIL", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := reviewToken(tc.text); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// The original completed review is retained and qualifies through real owning
// checks/review settlement without a reformat-only second model generation.
func TestReviewCapturedConclusionUsesOneReviewGeneration(t *testing.T) {
	for _, c := range capturedConclusionReviews(t) {
		t.Run(c.Source, func(t *testing.T) {
			workers, reviews := 0, 0
			model := roleHandoffModel(func(messages []llm.Message) (string, error) {
				if strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
					reviews++
					if reviews == 1 {
						return c.Content, nil
					}
					return "PASS: controlled format-only fallback", nil
				}
				workers++
				return "controlled component artifact", nil
			})
			d, aid, _, _ := roleHandoffFixture(t, 1, model)
			results, err := Launch(t.Context(), d, aid)
			if err != nil || len(results) != 1 || !results[0].Qualified() || results[0].Review != c.Content || workers != 1 || reviews != 1 {
				t.Fatalf("reformatted original verdict or bypassed owning checks: workers=%d reviews=%d result=%+v err=%v", workers, reviews, results, err)
			}
		})
	}
}
