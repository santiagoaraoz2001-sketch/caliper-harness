package launch

import (
	"encoding/json"
	"os"
	"testing"

	"specific-harness/internal/outcome"
)

func TestReviewMarkdownCapturedVerdicts(t *testing.T) {
	raw, err := os.ReadFile("testdata/reviewer-markdown-verdicts.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture struct{ Replies []string }
	if err := json.Unmarshal(raw, &capture); err != nil || len(capture.Replies) != 16 {
		t.Fatalf("invalid captured replies: %d %v", len(capture.Replies), err)
	}
	for i, reply := range capture.Replies {
		if token := reviewToken(reply); token != "PASS" || reviewVerdict(reply) != outcome.Passed {
			t.Errorf("captured reply %d: explicit Markdown verdict became %q", i, token)
		}
	}
}

func TestReviewBoldCapturedVerdicts(t *testing.T) {
	raw, err := os.ReadFile("testdata/reviewer-bold-verdicts.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture struct{ Replies []string }
	if err := json.Unmarshal(raw, &capture); err != nil || len(capture.Replies) != 6 {
		t.Fatalf("invalid captured replies: %d %v", len(capture.Replies), err)
	}
	for i, reply := range capture.Replies {
		if token := reviewToken(reply); token != "PASS" || reviewVerdict(reply) != outcome.Passed {
			t.Errorf("captured bold reply %d: explicit verdict became %q", i, token)
		}
	}
}

func TestReviewMarkdownLeadingVerdictOnly(t *testing.T) {
	for _, tc := range []struct {
		text  string
		token string
	}{
		{"# PASS\n\nObserved evidence.", "PASS"},
		{" \n##\tPASS ##\n\nObserved evidence.", "PASS"},
		{"   ## PASS\n\nObserved evidence.", "PASS"},
		{"###### UNVERIFIED\n\nChecks missing.", "UNVERIFIED"},
		{"## FAIL\n\nPASS requires fixing the observed defect.", "FAIL"},
		{"**PASS** — Observed evidence.", "PASS"},
		{"**FAIL:** Observed defect.", "FAIL"},
		{"__UNVERIFIED__: Missing evidence.", "UNVERIFIED"},
		{"*PASS* Evidence.", "PASS"},
		{"_FAIL_ Evidence.", "FAIL"},
		{"## **PASS**", "PASS"},
		{"PASS:confirmed", "PASS"},
		{"FAIL:defect", "FAIL"},
		{"UNVERIFIED:pending", "UNVERIFIED"},
		{"**PASS or FAIL**", ""},
		{"**PASS**/**FAIL**", ""},
		{"**PASS** / **FAIL**", ""},
		{"**PASS** or **FAIL**", ""},
		{"**PASS** **FAIL**", ""},
		{"    **PASS**", ""},
		{"\t**PASS**", ""},
		{"**PASS*", ""},
		{"`PASS` is an example", ""},
		{"> **PASS**", ""},
		{"## PASS FAIL\n\nConflicting verdicts.", ""},
		{"## FAIL / PASS", ""},
		{"> ## PASS\n\nQuoted example, not a verdict.", ""},
		{"```markdown\n## PASS\n```", ""},
		{"    ## PASS\n    Example", ""},
		{"\t## PASS\n\tExample", ""},
		{"Example: ## PASS", ""},
		{"Evidence first\n## PASS", ""},
		{"## PASSABLE", ""},
		{"## FAILURE", ""},
		{"## PASS:FAIL", ""},
		{"####### PASS", ""},
		{"#PASS", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got := reviewToken(tc.text)
			if tc.token != "" {
				if got != tc.token {
					t.Fatalf("verdict=%q want=%q", got, tc.token)
				}
			} else if got == "PASS" || got == "FAIL" || got == "UNVERIFIED" {
				t.Fatalf("non-verdict or ambiguous text became %q", got)
			}
		})
	}
}
