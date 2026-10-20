package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func TestAmbiguousCopiedPathCannotReadEditOrCreate(t *testing.T) {
	for _, name := range []string{"read", "edit", "write"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			input := `a%20b\ c`
			candidates := []string{`a b\ c`, `a%20b c`}
			const source = "original source\n"
			for _, candidate := range candidates {
				path := filepath.Join(root, candidate)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
			for _, def := range w.Definitions() {
				if def.Name != name {
					continue
				}
				args := tool.Args{"path": input, "old": source, "new": "changed\n", "content": "changed\n",
					"expected_hash": digest([]byte(source)), "offset": int64(1), "limit": int64(10)}
				normalized, err := def.NormalizeArgs(t.Context(), args)
				if err == nil {
					result, executionErr := def.Execute(t.Context(), normalized)
					t.Errorf("ambiguous path was admitted as %q: changed=%v error=%v", normalized["path"], result.Changed, executionErr)
				} else if !strings.Contains(err.Error(), "ambiguous") {
					t.Errorf("normalization should explain ambiguity: %v", err)
				}
			}
			for _, candidate := range candidates {
				body, err := os.ReadFile(filepath.Join(root, candidate))
				if err != nil || string(body) != source {
					t.Errorf("candidate %q changed: %q %v", candidate, body, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, input)); !os.IsNotExist(err) {
				t.Fatalf("absent authored spelling was created or inaccessible: %v", err)
			}
		})
	}
}

func TestCopiedPathNewFilesAndSymlinkAdmission(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	for _, def := range w.Definitions() {
		if def.Name != "write" {
			continue
		}
		args, err := def.NormalizeArgs(t.Context(), tool.Args{"path": "new%20name", "content": "new source"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := def.Execute(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		if body, err := os.ReadFile(filepath.Join(root, "new%20name")); err != nil || string(body) != "new source" {
			t.Fatalf("a genuinely new file changed spelling: %q %v", body, err)
		}
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link name")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	for _, def := range w.Definitions() {
		if def.Name != "write" {
			continue
		}
		args, err := def.NormalizeArgs(t.Context(), tool.Args{"path": "link%20name", "content": "bad", "expected_hash": digest([]byte("safe"))})
		if err == nil {
			_, err = def.Execute(t.Context(), args)
		}
		if err == nil {
			t.Fatal("decoded final symlink bypassed existing write admission")
		}
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "safe" {
		t.Fatalf("symlink target changed: %q %v", body, err)
	}
}
