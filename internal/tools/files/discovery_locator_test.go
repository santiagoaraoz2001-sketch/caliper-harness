package files

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/testfs"
	"strconv"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/search"
)

func TestDiscoveryAbsoluteGrepLocatorReadsCanonicalSource(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := "package fixture\nvar Needle = 1\n"
	if err := os.WriteFile(filepath.Join(root, "sub", "source.go"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	grep := search.NewGrepAt(cfg.Tools.Search, root)
	args, repair := tool.Validate(grep, map[string]string{"path": filepath.Join(root, "sub"), "pattern": "Needle", "literal": "true"})
	if repair != "" {
		t.Fatal(repair)
	}
	result, err := grep.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		Path  string
		Lines []struct{ Line int }
	}
	if err := json.Unmarshal([]byte(result.Text), &groups); err != nil || len(groups) != 1 || len(groups[0].Lines) != 1 || groups[0].Path != "sub/source.go" {
		t.Fatalf("actual canonical grep locator: %+v %v %s", groups, err, result.Text)
	}
	w := &Workspace{Path: root, Caps: cfg.Tools.Bash}
	read, err := invokeDefinitions(t, w.Definitions(), "read", map[string]string{"path": groups[0].Path, "offset": strconv.Itoa(groups[0].Lines[0].Line), "limit": "1"})
	if err != nil || !strings.HasPrefix(read.Text, filepath.Join("sub", "source.go")+" sha256="+digest([]byte(raw))) || !strings.Contains(read.Text, "\nvar Needle = 1\n") || !strings.Contains(read.Text, "shown_range=2-2") {
		t.Fatalf("returned grep locator cannot be read as canonical source: %+v %v", read, err)
	}
}

func TestDiscoveryRelativeAndAbsoluteReadsShareCurrentCoverage(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	defs, sibling := w.Definitions(), w.Definitions()
	path := filepath.Join(root, "source.txt")
	raw := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, page := range []struct{ path, offset string }{{"./source.txt", "1"}, {path, "2"}} {
		result, err := invokeDefinitions(t, defs, "read", map[string]string{"path": page.path, "offset": page.offset, "limit": "1"})
		if err != nil || !strings.HasPrefix(result.Text, "source.txt sha256="+digest([]byte(raw))) {
			t.Fatalf("same source locator: %+v %v", result, err)
		}
	}
	edit := map[string]string{"path": "source.txt", "start_line": "1", "end_line": "2", "new": "joined\n", "expected_hash": digest([]byte(raw))}
	if result, err := invokeDefinitions(t, sibling, "edit", edit); err == nil || result.Changed {
		t.Fatal("absolute discovery widened coverage to a sibling registry")
	}
	if result, err := invokeDefinitions(t, defs, "edit", edit); err != nil || !result.Changed {
		t.Fatalf("relative/absolute pages did not combine for the same source: %+v %v", result, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "joined\nthree\n" {
		t.Fatalf("source effect: %q %v", current, err)
	}
	if _, err := invokeDefinitions(t, defs, "read", map[string]string{"path": path, "limit": "1"}); err != nil {
		t.Fatal(err)
	}
	// A sibling publication followed by restoration invalidates an old grant even at the same hash.
	for _, content := range []string{"temporary\n", string(current)} {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := invokeDefinitions(t, sibling, "write", map[string]string{"path": "source.txt", "content": content, "expected_hash": digest(before)}); err != nil {
			t.Fatal(err)
		}
	}
	one := map[string]string{"path": "source.txt", "start_line": "1", "end_line": "1", "new": "fresh\n", "expected_hash": digest(current)}
	if result, err := invokeDefinitions(t, defs, "edit", one); err == nil || result.Changed {
		t.Fatal("absolute read retained stale publication coverage")
	}
	if _, err := invokeDefinitions(t, defs, "read", map[string]string{"path": path, "limit": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeDefinitions(t, defs, "edit", one); err != nil {
		t.Fatalf("current absolute reread did not restore the observed range: %v", err)
	}
	// Absolute mutation aliases use the same current source guard.
	for _, name := range []string{"edit", "write"} {
		before, _ := os.ReadFile(path)
		slots := map[string]string{"path": path, "expected_hash": digest(before)}
		want := "absolute replacement\n"
		if name == "edit" {
			slots["old"], slots["new"] = "fresh", "absolute"
			want = strings.Replace(string(before), "fresh", "absolute", 1)
		} else {
			slots["content"] = want
		}
		result, err := invokeDefinitions(t, defs, name, slots)
		after, _ := os.ReadFile(path)
		if err != nil || !result.Changed || string(after) != want || result.FileEffect.Path != "source.txt" {
			t.Fatalf("absolute alias %s: %+v %v", name, result, err)
		}
	}

}

func TestDiscoveryAbsoluteDirectoriesAndNavigationRetainCoverageBoundaries(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	raw := "package fixture\n\n// Work docs.\nfunc Work() {}\n\nfunc Hidden() {}\n"
	path := filepath.Join(root, "source.go")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	relative, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := invokeDefinitions(t, defs, "read", map[string]string{"path": root})
	if err != nil || relative.Text != absolute.Text || !strings.Contains(absolute.Text, `file "source.go"`) || !strings.Contains(absolute.Text, `dir "nested"`) {
		t.Fatalf("absolute workspace root differs: %+v %v", absolute, err)
	}
	find := w.FindDirDefinition()
	dirs, err := outsideCall(t, find, context.Background(), map[string]string{"path": root})
	if err != nil || !strings.Contains(dirs.Text, `Directory "."`) || !strings.Contains(dirs.Text, `dir "nested"`) || strings.Contains(dirs.Text, `file "source.go"`) {
		t.Fatalf("canonical directory navigation: %+v %v", dirs, err)
	}
	outline, err := navigationCall(t, defs, "read", map[string]any{"path": path, "outline": true})
	if err != nil || !strings.HasPrefix(outline.Text, "source.go sha256="+digest([]byte(raw))) || !strings.Contains(outline.Text, `definition="Work"`) || len(w.publications) != 0 {
		t.Fatalf("absolute outline/source distinction: %+v %v", outline, err)
	}
	work := map[string]any{"path": "source.go", "start_line": 3, "end_line": 4, "new": "// Work docs.\nfunc Work() { }\n", "expected_hash": digest([]byte(raw))}
	if result, err := navigationCall(t, defs, "edit", work); err == nil || result.Changed {
		t.Fatal("directory/outline metadata granted source coverage")
	}
	definition, err := navigationCall(t, defs, "read", map[string]any{"path": path, "definition": "Work"})
	if err != nil || !strings.Contains(definition.Text, "shown_range=3-4") || !strings.Contains(definition.Text, "selection_complete=true") || !strings.Contains(definition.Text, "EOF=false") {
		t.Fatalf("absolute definition: %+v %v", definition, err)
	}
	hidden := map[string]any{"path": "source.go", "start_line": 6, "end_line": 6, "new": "func Hidden() { }\n", "expected_hash": digest([]byte(raw))}
	if result, err := navigationCall(t, defs, "edit", hidden); err == nil || result.Changed {
		t.Fatal("absolute definition granted unseen source")
	}
	if result, err := navigationCall(t, defs, "edit", work); err != nil || !result.Changed {
		t.Fatalf("absolute definition source did not authorize its canonical range: %+v %v", result, err)
	}
}

func TestDiscoveryPhysicalAliasesEscapesAndFinalSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("package fixture\nfunc Work() {}\n")
	for _, path := range []string{filepath.Join(root, "sub", "source.go"), filepath.Join(outside, "source.go")} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	aliasRoot := filepath.Join(t.TempDir(), "workspace-alias")
	for link, target := range map[string]string{aliasRoot: root, filepath.Join(root, "alias"): filepath.Join(root, "sub"), filepath.Join(root, "escape"): outside, filepath.Join(root, "dir-link"): filepath.Join(root, "sub")} {
		if err := testfs.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	fileLink := testfs.FileSymlink(t, filepath.Join(root, "sub", "source.go"), filepath.Join(root, "file-link"))
	w := &Workspace{Path: aliasRoot, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	for _, path := range []string{"alias/source.go", filepath.Join(root, "alias", "source.go"), filepath.Join(aliasRoot, "sub", "source.go")} {
		result, err := invokeDefinitions(t, defs, "read", map[string]string{"path": path})
		if err != nil || !strings.HasPrefix(result.Text, filepath.Join("sub", "source.go")+" sha256="+digest(raw)) {
			t.Fatalf("physical aliases do not share a canonical source locator: %+v %v", result, err)
		}
	}
	for _, path := range []string{filepath.Join(outside, "source.go"), filepath.Join(root, "escape", "source.go"), "../source.go", filepath.Join(root, "file-link"), filepath.Join(root, "dir-link")} {
		if path == filepath.Join(root, "file-link") && !fileLink {
			continue
		}
		result, err := invokeDefinitions(t, defs, "read", map[string]string{"path": path})
		if err == nil || result.Text != "" || result.Changed {
			t.Errorf("ordinary absolute discovery crossed scope/symlink boundary %s: %+v %v", path, result, err)
		}
	}
	for _, path := range []string{outside, filepath.Join(root, "escape"), filepath.Join(root, "dir-link")} {
		result, err := outsideCall(t, w.FindDirDefinition(), context.Background(), map[string]string{"path": path})
		if err == nil || result.Text != "" {
			t.Errorf("directory scope/symlink boundary %s: %+v %v", path, result, err)
		}
	}
	for _, d := range w.OutsideDefinitions(config.Defaults().Tools.Search) {
		if d.Name != "read_outside" {
			continue
		}
		result, err := outsideCall(t, d, context.Background(), map[string]string{"path": filepath.Join(outside, "source.go")})
		if err != nil || !strings.Contains(result.Text, "func Work()") || result.Changed || len(w.publications) != 1 {
			t.Fatalf("explicit outside discovery parity/current coverage: %+v %v", result, err)
		}
	}
}
