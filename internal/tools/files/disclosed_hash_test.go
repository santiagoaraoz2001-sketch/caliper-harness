package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
)

// A whole-file write that omits expected_hash is refused with the current
// hash disclosed. An immediate retry against unchanged bytes must succeed
// without the caller transcribing the 64-hex hash: the refusal itself
// disclosed it, and the digest is still verified at apply time. (c52: six
// refused whole-file regenerations for the same omission.)
func TestWriteRetryUsesDisclosedHash(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	p := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(p, []byte("old content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "target.txt", "content": "new content\n"}); err == nil || !strings.Contains(err.Error(), "exists; current sha256=") {
		t.Fatalf("first write not refused with disclosed hash: %v", err)
	}
	res, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "target.txt", "content": "new content\n"})
	if err != nil || !res.Changed {
		t.Fatalf("immediate retry did not use the disclosed hash: %+v %v", res, err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "new content\n" {
		t.Fatalf("retry wrote %q", got)
	}
	// The disclosed hash is single-use: a further hashless write refuses again.
	if _, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "target.txt", "content": "third\n"}); err == nil {
		t.Fatal("disclosed hash was reused after a successful write")
	}
}

// If the file changes between disclosure and retry, the retry must refuse
// again: the carry never overwrites bytes other than those disclosed.
func TestWriteDisclosedHashInvalidatedByExternalChange(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	p := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(p, []byte("old content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "target.txt", "content": "new content\n"}); err == nil {
		t.Fatal("first write not refused")
	}
	if err := os.WriteFile(p, []byte("externally changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeDefinitions(t, defs, "write", map[string]string{"path": "target.txt", "content": "new content\n"}); err == nil {
		t.Fatal("retry overwrote bytes changed after disclosure")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "externally changed\n" {
		t.Fatalf("external change lost: %q", got)
	}
}
