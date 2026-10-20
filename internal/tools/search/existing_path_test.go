package search

import (
	"os"
	"path/filepath"
	"strings"
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
	if got, err := ExistingPath(root, escaped); err != nil || got != real {
		t.Fatalf("escaped path not resolved: got %q want %q error %v", got, real, err)
	}
	if got, err := ExistingPath(root, real); err != nil || got != real {
		t.Fatalf("existing literal path changed: %q %v", got, err)
	}
	missing := filepath.Join(root, "Specific\\ Labs", "missing.js")
	if got, err := ExistingPath(root, missing); err != nil || got != missing {
		t.Fatalf("unresolvable escaped path must pass through: got %q error %v", got, err)
	}
}

func TestExistingPathLiteralPrecedenceAndOnePass(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a b", "a%20b"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for input, want := range map[string]string{
		"a%20b":       "a%20b",
		"a%2520b":     "a%20b",
		"new%20name":  "new%20name",
		"https://a/b": "https://a/b",
	} {
		if got, err := ExistingPath(root, input); err != nil || got != want {
			t.Errorf("%q resolved to %q, want %q: %v", input, got, want, err)
		}
	}
}

func TestExistingPathAmbiguityNamesBothCandidates(t *testing.T) {
	root := t.TempDir()
	candidates := []string{`a b\ c`, `a%20b c`}
	for _, candidate := range candidates {
		path := filepath.Join(root, candidate)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ExistingPath(root, `a%20b\ c`)
	if got != "" || err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), `"a b\\ c"`) || !strings.Contains(err.Error(), `"a%20b c"`) {
		t.Fatalf("ambiguous lookup failed to name its alternatives: %q %v", got, err)
	}
	// Once that exact literal exists it wins, on both slash-based filesystems
	// and Windows where the backslash is an actual directory separator.
	literal := filepath.Join(root, `a%20b\ c`)
	if err := os.MkdirAll(filepath.Dir(literal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(literal, []byte("literal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ExistingPath(root, `a%20b\ c`); err != nil || got != `a%20b\ c` {
		t.Fatalf("literal precedence lost: %q %v", got, err)
	}
}

func TestExistingPathDoesNotFollowFinalSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link name")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if got, err := ExistingPath(root, "link%20name"); err != nil || got != "link name" {
		t.Fatalf("normalizer must preserve final-link identity for owning admission: %q %v", got, err)
	}
}
