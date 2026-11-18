package bash

import (
	"context"
	"strings"
	"testing"
)

// Structured callers emit every advertised slot, so a stray cmd on
// service=stop/status must not refuse the action: the command is never
// executed for those actions, and the result must say it was ignored.
// (c52: three refused stops across two sessions before cmd:"" was guessed.)
func TestServiceStopStatusIgnoreStrayCmd(t *testing.T) {
	owner := &ServiceOwner{}
	res, err := owner.execute(context.Background(), "status", "echo should-not-run", t.TempDir())
	if err != nil {
		t.Fatalf("status with stray cmd refused: %v", err)
	}
	if !strings.Contains(res.Text, "No running owned session service") {
		t.Fatalf("status text changed: %q", res.Text)
	}
	if !strings.Contains(res.Text, "cmd ignored") || !strings.Contains(res.Text, "not executed") {
		t.Fatalf("status did not disclose the ignored cmd: %q", res.Text)
	}
	res, err = owner.execute(context.Background(), "stop", "echo should-not-run", t.TempDir())
	if err != nil {
		t.Fatalf("stop with stray cmd refused: %v", err)
	}
	if !strings.Contains(res.Text, "Owned session service stopped.") {
		t.Fatalf("stop text changed: %q", res.Text)
	}
	if !strings.Contains(res.Text, "cmd ignored") || !strings.Contains(res.Text, "not executed") {
		t.Fatalf("stop did not disclose the ignored cmd: %q", res.Text)
	}
	// Without a stray cmd the results stay clean.
	res, err = owner.execute(context.Background(), "stop", "", t.TempDir())
	if err != nil || res.Text != "Owned session service stopped." {
		t.Fatalf("plain stop changed: %q %v", res.Text, err)
	}
	// Start still requires a real command.
	if _, err = owner.execute(context.Background(), "start", "  ", t.TempDir()); err == nil {
		t.Fatal("service=start accepted an empty cmd")
	}
}
