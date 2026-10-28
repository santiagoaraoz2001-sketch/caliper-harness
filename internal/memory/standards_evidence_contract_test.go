package memory

import (
	"strings"
	"testing"
)

func TestEvidenceGuidanceStaysInExistingSearchableSections(t *testing.T) {
	for _, tc := range []struct{ body, section, needle string }{
		{CodeStandard, "state", "non-default valid state"},
		{TestsStandard, "execution", "required stage must fail"},
		{CodeStandard, "evidence", "does not prove its cause"},
		{CodeStandard, "evidence", "derived example outputs as expected and unexecuted"},
		{UIStandard, "verify", "parent visibility and modal state"},
		{UIStandard, "verify", "actual composited background"},
		{UIStandard, "copy", "actual prerequisites and handlers"},
	} {
		parts := Sections(tc.body, "", tc.section)
		if len(parts) != 1 || !strings.Contains(parts[0].Body, tc.needle) {
			t.Errorf("guidance not reachable in %s: %+v", tc.section, parts)
		}
	}
}
