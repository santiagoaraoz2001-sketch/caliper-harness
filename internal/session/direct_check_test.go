package session

import "testing"

func TestDirectReceiptCannotQualifyHistoricalPairedState(t *testing.T) {
	zero := 0
	c := &CodingState{TaskID: "direct", Workspace: "work", Command: "check", Receipt: &CheckReceipt{Direct: true, Command: "check", Cwd: "work", Started: "start", Ended: "end", EpisodeStarted: "start", EpisodeEnded: "end", Status: "passed", ExitCode: &zero}}
	if !c.Passed() || c.Validate() != nil {
		t.Fatal("new direct receipt rejected")
	}
	c.Receipt.Direct = false
	if c.Passed() {
		t.Fatal("legacy untagged receipt qualified")
	}
	c.Receipt.Direct = true
	c.Attempt = 1
	if c.Passed() || c.Validate() == nil {
		t.Fatal("paired identity downgraded")
	}
	c.Attempt = 0
	c.AcceptanceCommand = "behavior"
	if c.Passed() || c.Validate() == nil {
		t.Fatal("frozen behavior bypassed")
	}
	c.AcceptanceCommand = ""
	c.Invalidate("later effect")
	if c.Passed() {
		t.Fatal("stale direct pass")
	}
}
