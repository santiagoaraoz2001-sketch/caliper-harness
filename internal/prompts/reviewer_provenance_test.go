package prompts

import (
	"strings"
	"testing"
)

// ST03/ST06 (v024 Space Transporter): a failing test followed by successful
// shell commands settled as bash exit 0, and the executor relaxed its own
// acceptance-test threshold before the suite passed. The reviewer contract
// must therefore state that composite shell output is not check evidence
// and that a changed test does not validate its own change.
func TestReviewerRoleCarriesTestProvenanceContract(t *testing.T) {
	text := strings.Join(strings.Fields(Builtin().texts["agent.reviewer_role"]), " ")
	for _, want := range []string{
		"composite shell output is not a passing check",
		"only bound check receipts establish",
		"changed test, fixture or check files",
		"a passing changed test does not validate its own change",
		"unsupported relaxation is a defect",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("agent.reviewer_role omits %q", want)
		}
	}
}
