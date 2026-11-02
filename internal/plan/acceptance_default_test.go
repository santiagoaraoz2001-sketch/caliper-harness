package plan

import "testing"

// One command, one source: omitting the acceptance command binds the
// task check command instead of forcing the author to restate the same
// command as a second copy that can silently diverge.
func TestSetTaskBlockDefaultsAcceptanceToCheckCommand(t *testing.T) {
	s := open(t)
	p, e := CreatePlan(s, "p", "g")
	if e != nil {
		t.Fatal(e)
	}
	a, e := CreateApproach(s, p.ID, "a")
	if e != nil {
		t.Fatal(e)
	}
	x, e := CreateTask(s, a.ID, "x", "do it")
	if e != nil {
		t.Fatal(e)
	}
	got, e := SetTaskBlock(s, x.ID, "c", "t", "s", "acc", "tests", "verify", "npm run check")
	if e != nil {
		t.Fatal(e)
	}
	if got.CheckCommand != "npm run check" || got.AcceptanceCommand != "npm run check" {
		t.Fatalf("commands check=%q acceptance=%q, want both bound to the check command", got.CheckCommand, got.AcceptanceCommand)
	}
	// An explicitly distinct acceptance command is preserved.
	got, e = SetTaskBlock(s, x.ID, "c", "t", "s", "acc", "tests", "verify", "npm run check", "node tools/run-tests.mjs")
	if e != nil {
		t.Fatal(e)
	}
	if got.AcceptanceCommand != "node tools/run-tests.mjs" {
		t.Fatalf("distinct acceptance overwritten: %q", got.AcceptanceCommand)
	}
}
