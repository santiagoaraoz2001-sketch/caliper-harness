package files

import (
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"testing"
)

// Creating a file whose parent directories do not exist yet is ordinary
// file creation, not a scope violation: the parents are created inside
// the workspace root. Confinement is unchanged (see grounding tests for
// linked-parent refusals).
func TestWriteCreatesMissingParentDirectories(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	res, e := invoke(t, w, "write", map[string]string{"path": "src/core/rng.js", "content": "export const x = 1;\n"})
	if e != nil || !res.Changed {
		t.Fatalf("nested create %+v %v", res, e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "src", "core", "rng.js"))
	if e != nil || string(b) != "export const x = 1;\n" {
		t.Fatalf("nested bytes %q %v", b, e)
	}
	// Escapes still refused at admission.
	if _, e := invoke(t, w, "write", map[string]string{"path": "../escape.txt", "content": "x"}); e == nil {
		t.Fatal("parent escape accepted")
	}
}
