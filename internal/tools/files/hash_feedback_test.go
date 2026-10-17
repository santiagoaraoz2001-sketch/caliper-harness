package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

// A mistyped expected_hash on an unchanged file must say so (with the
// issued hash, no reread), and a unique exact-span edit needs no hash.
func TestEditHashFeedbackAndOptionalHash(t *testing.T) {
	const original = "alpha\nbeta\ngamma\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	reg := tool.NewRegistry()
	for _, def := range w.Definitions() {
		reg.Register(def)
	}
	invoke := func(name string, args map[string]any) (tool.Result, error) {
		t.Helper()
		def, _ := reg.Lookup(name)
		return def.Execute(context.Background(), args)
	}
	hash := digest([]byte(original))
	if _, err := invoke("read", map[string]any{"path": "note.txt", "offset": int64(1), "limit": int64(500)}); err != nil {
		t.Fatal(err)
	}
	// Mistyped hash, file unchanged: name the issued hash, no reread demand.
	_, err := invoke("edit", map[string]any{"path": "note.txt", "old": "beta", "new": "BETA", "expected_hash": "deadbeef"})
	if err == nil || !strings.Contains(err.Error(), "unchanged since your read") || !strings.Contains(err.Error(), hash) {
		t.Fatalf("mistyped hash feedback: %v", err)
	}
	// Optional hash: unique exact span applies without expected_hash.
	res, err := invoke("edit", map[string]any{"path": "note.txt", "old": "beta", "new": "BETA"})
	if err != nil || !res.Changed {
		t.Fatalf("hashless unique edit: %+v %v", res, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("content: %q", b)
	}
	// Genuinely changed file with the previously issued hash: changed msg.
	oldHash := hash
	if err := os.WriteFile(path, []byte("alpha\nBETA\ndelta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = invoke("edit", map[string]any{"path": "note.txt", "old": "BETA", "new": "beta", "expected_hash": oldHash})
	if err == nil || !strings.Contains(err.Error(), "changed since your read") {
		t.Fatalf("changed file feedback: %v", err)
	}
	// Range edits still require the hash.
	_, err = invoke("edit", map[string]any{"path": "note.txt", "start_line": int64(1), "end_line": int64(1), "new": "ALPHA\n"})
	if err == nil || !strings.Contains(err.Error(), "needs expected_hash") {
		t.Fatalf("range hash requirement: %v", err)
	}
}
