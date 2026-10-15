package files

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func outsideCall(t *testing.T, d tool.Definition, ctx context.Context, slots map[string]string) (tool.Result, error) {
	t.Helper()
	a, repair := tool.Validate(d, slots)
	if repair != "" {
		t.Fatal(repair)
	}
	return d.Execute(ctx, a)
}

func TestExplicitOutsideDiscovery(t *testing.T) {
	cfg := config.Defaults()
	root, other := t.TempDir(), t.TempDir()
	other, err := filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("one\ntwo\nthree\n")
	for _, dir := range []string{root, other} {
		if err := os.WriteFile(filepath.Join(dir, "source.txt"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"nested", ".hidden", "ignored"} {
		if err := os.Mkdir(filepath.Join(other, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "nested", "deep.txt"), []byte("deep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, ".gitignore"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "ignored", "excluded.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(other, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "source.txt"), filepath.Join(other, "file-link")); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Path: root, Caps: cfg.Tools.Bash}
	defs := w.OutsideDefinitions(cfg.Tools.Search)
	read, find := defs[0], defs[1]
	t.Run("file page", func(t *testing.T) {
		res, err := outsideCall(t, read, context.Background(), map[string]string{"path": filepath.Join(other, "source.txt"), "limit": "1"})
		if err != nil || !res.Truncated || !strings.Contains(res.Text, "\none\n") || strings.Contains(res.Text, "\ntwo\n") || !strings.Contains(res.Text, "next_offset=2") || !strings.HasPrefix(res.Text, other) || len(res.Text) > cfg.Tools.Bash.MaxBytes || res.Changed || res.FileEffect != nil {
			t.Fatalf("%+v %v", res, err)
		}
		next, err := outsideCall(t, read, context.Background(), map[string]string{"path": filepath.Join(other, "source.txt"), "offset": "2"})
		if err != nil || !strings.Contains(next.Text, "\ntwo\nthree\n") || next.Truncated {
			t.Fatalf("%+v %v", next, err)
		}
	})
	t.Run("directory page", func(t *testing.T) {
		res, err := outsideCall(t, read, context.Background(), map[string]string{"path": other, "limit": "1"})
		if err != nil || !res.Truncated || !strings.Contains(res.Text, "next_offset=2") || !strings.Contains(res.Text, other) || strings.Contains(res.Text, "sha256=") {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("directory navigation", func(t *testing.T) {
		res, err := outsideCall(t, find, context.Background(), map[string]string{"path": other, "mode": "dir", "pattern": "*"})
		if err != nil || !strings.Contains(res.Text, ".hidden") || !strings.Contains(res.Text, "ignored") || strings.Contains(res.Text, "inside-link") || strings.Contains(res.Text, "deep.txt") || !strings.Contains(res.Text, "read_outside(path=") {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("recursive files", func(t *testing.T) {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip(err)
		}
		cmd := exec.Command("git", "init", "-q", other)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init %s %v", out, err)
		}
		res, err := outsideCall(t, find, context.Background(), map[string]string{"path": other, "mode": "file", "pattern": "*.txt"})
		if err != nil || !strings.Contains(res.Text, "deep.txt") || strings.Contains(res.Text, "excluded.txt") || strings.Contains(res.Text, "inside-link") || !strings.HasPrefix(res.Text, other) {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("scope refusals", func(t *testing.T) {
		for _, d := range defs {
			for _, p := range []string{".", "../other", root, filepath.Join(root, "source.txt"), filepath.Join(other, "inside-link"), filepath.Join(other, "file-link"), filepath.Join(other, "inside-link", "source.txt")} {
				slots := map[string]string{"path": p}
				if d.Name == "find_outside" {
					slots["mode"] = "dir"
					slots["pattern"] = "*"
				}
				res, err := outsideCall(t, d, context.Background(), slots)
				if err == nil || res.Text != "" {
					t.Errorf("%s %s admitted: %+v %v", d.Name, p, res, err)
				}
			}
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, d := range defs {
			slots := map[string]string{"path": other}
			if d.Name == "find_outside" {
				slots["mode"] = "file"
				slots["pattern"] = "*"
			}
			res, err := outsideCall(t, d, ctx, slots)
			if !errors.Is(err, context.Canceled) || res.Text != "" {
				t.Fatalf("%+v %v", res, err)
			}
		}
	})
	t.Run("no edit authority", func(t *testing.T) {
		if len(w.publications) != 0 {
			t.Fatalf("outside read published workspace authority: %v", w.publications)
		}
		defs := w.Definitions()
		for _, d := range defs {
			if d.Name == "edit" {
				for _, p := range []string{"source.txt", filepath.Join(other, "source.txt"), "outside-link/source.txt"} {
					res, err := outsideCall(t, d, context.Background(), map[string]string{"path": p, "start_line": "1", "end_line": "1", "new": "changed\n", "expected_hash": digest(body)})
					if err == nil || res.Changed {
						t.Fatalf("outside read authorized edit %s: %+v %v", p, res, err)
					}
				}
			}
		}
		after, err := os.ReadFile(filepath.Join(other, "source.txt"))
		if err != nil || string(after) != string(body) {
			t.Fatalf("outside file changed: %q %v", after, err)
		}
	})
}
