package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
)

// A file that appeared outside the file tools (e.g. written by bash) must
// not force a delete-and-regenerate cycle: the create refusal names the
// current hash, and a whole-file replacement pinned to that hash proceeds.
// Line-range grounding (observed ranges) is unchanged.
func TestExistingCreateRefusalDisclosesCurrentHash(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	raw := "created by a shell command\n"
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "a", "content": "replacement\n"})
	if err == nil || !strings.Contains(err.Error(), "sha256="+digest([]byte(raw))) {
		t.Fatalf("create refusal must disclose current hash: %v", err)
	}
	res, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "a", "content": "replacement\n", "expected_hash": digest([]byte(raw))})
	if err != nil || !res.Changed {
		t.Fatalf("hash-pinned replacement refused: %+v %v", res, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a"))
	if string(got) != "replacement\n" {
		t.Fatalf("bytes=%q", got)
	}
}
