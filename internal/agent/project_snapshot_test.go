package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"testing"
)

// Snapshot/restore must undo exactly what a check episode wrote:
// created artifact files and dirs removed, modified files restored,
// deleted files recreated — everything else untouched.
func TestProjectSnapshotRestoreUndoesCheckWrites(t *testing.T) {
	root := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "keep")
	write("src/main.js", "original")
	r := &agent.Runner{Workspace: root}
	snap, err := r.CaptureWorkspaceSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	write(".shots/lane.png", "pngbytes")
	write("src/main.js", "tampered")
	if err := os.Remove(filepath.Join(root, "keep.txt")); err != nil {
		t.Fatal(err)
	}
	changed, err := snap.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("changed=%v want 3 paths", changed)
	}
	if _, err := os.Stat(filepath.Join(root, ".shots")); !os.IsNotExist(err) {
		t.Fatalf("created artifact dir not pruned: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src/main.js")); string(b) != "original" {
		t.Fatalf("modified file not restored: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(b) != "keep" {
		t.Fatalf("deleted file not recreated: %q", b)
	}
	before, _ := r.WorkspaceFingerprint(context.Background())
	again, err := snap.Restore(context.Background())
	if err != nil || len(again) != 0 {
		t.Fatalf("second restore not a no-op: %v %v", again, err)
	}
	after, _ := r.WorkspaceFingerprint(context.Background())
	if before != after {
		t.Fatal("fingerprint drifted across no-op restore")
	}
}
