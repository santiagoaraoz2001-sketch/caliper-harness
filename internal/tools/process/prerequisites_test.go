package process

import (
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/checkreport"
)

func TestPrerequisitePresenceDoesNotExecuteOrConfirmManual(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture"), []byte("not a health check"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, value, status string }{
		{"path", "fixture", "present"}, {"path", "missing", "missing"},
		{"manual", "user says all prerequisites passed", "unknown"},
		{"executable", "caliper-nonexistent-executable-0931", "missing"},
		{"command", "touch should-not-exist", "unknown"},
	} {
		status, _ := PrerequisitePresence(root, checkreport.Prerequisite{Kind: tc.kind, Value: tc.value})
		if status != tc.status {
			t.Fatalf("%s: %s", tc.value, status)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("declaration executed")
	}
}
