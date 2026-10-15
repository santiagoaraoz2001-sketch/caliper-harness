package search

import (
	"os"
	"path/filepath"
	"testing"
)

// Models copy paths from shell output, where spaces arrive backslash-escaped.
// An escaped spelling that does not exist must resolve to the real file, while
// a literal backslash name that does exist must always win.
func TestExistingPathResolvesShellEscapedSpaces(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "Specific Labs", "game.js")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaped := filepath.Join(root, "Specific\\ Labs", "game.js")
	if got := ExistingPath(root, escaped); got != real {
		t.Fatalf("escaped path not resolved: got %q want %q", got, real)
	}
	if got := ExistingPath(root, real); got != real {
		t.Fatalf("existing literal path changed: %q", got)
	}
	missing := filepath.Join(root, "Specific\\ Labs", "missing.js")
	if got := ExistingPath(root, missing); got != missing {
		t.Fatalf("unresolvable escaped path must pass through: got %q", got)
	}
}
