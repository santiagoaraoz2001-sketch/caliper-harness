package outcome

import "testing"

func TestExitAndQualification(t *testing.T) {
	for _, tc := range []struct {
		status Status
		code   int
	}{{Unknown, 1}, {Completed, 0}, {Stopped, 1}, {Failed, 1}, {Cancelled, 130}} {
		if got := (Outcome{Status: tc.status}).ExitCode(); got != tc.code {
			t.Errorf("%s code=%d", tc.status, got)
		}
		for _, v := range []Verification{Unverified, Passed, Rejected, NotRequired} {
			want := tc.status == Completed && (v == Passed || v == NotRequired)
			if Qualified(tc.status, v) != want {
				t.Errorf("qualified %s/%s", tc.status, v)
			}
		}
	}
	if Valid(Unknown, Passed) || Valid(Stopped, Passed) || Valid("invented", Unverified) || Valid(Completed, "invented") {
		t.Fatal("invalid facts admitted")
	}
}
