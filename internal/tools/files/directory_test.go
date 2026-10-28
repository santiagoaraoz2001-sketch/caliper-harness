package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"specific-harness/internal/config"
	"specific-harness/internal/testfs"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/search"
	"strconv"
	"strings"
	"testing"
)

func TestDirectoryReadNavigationAndAuthority(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "tui"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("first\nsecond\n")
	unsafeName := "unsafe\n\x1bβ.go"
	if runtime.GOOS == "windows" {
		unsafeName = "unsafe β.go"
	}
	for _, name := range []string{"app.go", unsafeName} {
		if err := os.WriteFile(filepath.Join(root, "internal", "tui", name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fileLink := testfs.FileSymlink(t, "app.go", filepath.Join(root, "internal", "tui", "link"))
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	reg := tool.NewRegistry()
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
	reg.Register(search.NewFindAt(config.Defaults().Tools.Search, root))
	call := func(name string, args map[string]any) (tool.Result, error) {
		t.Helper()
		d, ok := reg.Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		b, _ := json.Marshal(args)
		a, refusal := tool.ValidateJSON(d, string(b))
		if refusal != "" {
			t.Fatal(refusal)
		}
		return d.Execute(context.Background(), a)
	}
	result, err := call("read", map[string]any{"path": ".", "limit": 1})
	if err != nil || result.Changed || strings.Contains(result.Text, "sha256") || !strings.Contains(result.Text, `"internal"`) {
		t.Fatalf("directory navigation unavailable: %+v %v", result, err)
	}
	seen := map[string]string{}
	offset := 1
	cursor := regexp.MustCompile(`next_offset=(\d+)`)
	for {
		page, e := call("read", map[string]any{"path": "internal/tui", "offset": offset, "limit": 1})
		if e != nil || len(page.Text) > w.Caps.MaxBytes || page.Changed || strings.Contains(page.Text, "sha256") {
			t.Fatalf("page %+v %v", page, e)
		}
		if !strings.Contains(page.Text, "live directory") {
			t.Fatal(page.Text)
		}
		for _, line := range strings.Split(page.Text, "\n") {
			kind, quoted, ok := strings.Cut(line, " ")
			if kind != "file" && kind != "dir" && kind != "symlink" && kind != "other" {
				continue
			}
			if !ok {
				t.Fatal(line)
			}
			var path string
			if e = json.Unmarshal([]byte(quoted), &path); e != nil {
				t.Fatal(e)
			}
			if _, exists := seen[filepath.ToSlash(path)]; exists {
				t.Fatalf("duplicate page path %q", path)
			}
			seen[filepath.ToSlash(path)] = kind
		}
		m := cursor.FindStringSubmatch(page.Text)
		if !page.Truncated {
			if len(m) > 0 {
				t.Fatal("cursor on EOF")
			}
			break
		}
		if len(m) != 2 {
			t.Fatal("missing cursor", page.Text)
		}
		next, _ := strconv.Atoi(m[1])
		if next != offset+1 {
			t.Fatal(page.Text)
		}
		offset = next
	}
	wantEntries := 2
	if fileLink {
		wantEntries++
	}
	if len(seen) != wantEntries || seen["internal/tui/app.go"] != "file" || fileLink && seen["internal/tui/link"] != "symlink" || seen["internal/tui/"+unsafeName] != "file" {
		t.Fatalf("paths lost/corrupted: %#v", seen)
	}
	absent, e := call("find", map[string]any{"pattern": "*tui*"})
	if e != nil || !strings.Contains(absent.Text, "basenames, not directories") || strings.Contains(absent.Text, "internal/tui/") {
		t.Fatalf("directory guess fabricated filename match: %+v %v", absent, e)
	}
	found, e := call("find", map[string]any{"pattern": "*.go", "path": "internal/tui"})
	if e != nil || !strings.Contains(found.Text, "internal/tui/app.go") {
		t.Fatalf("actual directory path query failed: %+v %v", found, e)
	}

	if _, e := call("edit", map[string]any{"path": "internal/tui/app.go", "start_line": 1, "end_line": 1, "new": "changed\n", "expected_hash": digest(raw)}); e == nil || !strings.Contains(e.Error(), "not returned") {
		t.Fatalf("directory granted source authority: %v", e)
	}
	file, e := call("read", map[string]any{"path": "internal/tui/app.go", "limit": 1})
	if e != nil || !strings.Contains(file.Text, digest(raw)) || !strings.Contains(file.Text, "\nfirst\n") {
		t.Fatalf("file reader changed: %+v %v", file, e)
	}
	if _, e = call("edit", map[string]any{"path": "internal/tui/app.go", "start_line": 1, "end_line": 1, "new": "changed\n", "expected_hash": digest(raw)}); e != nil {
		t.Fatal(e)
	}
	empty := filepath.Join(root, "empty")
	if e = os.Mkdir(empty, 0700); e != nil {
		t.Fatal(e)
	}
	page, e := call("read", map[string]any{"path": "empty"})
	if e != nil || page.Truncated || !strings.Contains(page.Text, "0 entries") {
		t.Fatalf("empty %+v %v", page, e)
	}
	page, e = call("read", map[string]any{"path": "internal/tui", "offset": 999})
	if e != nil || page.Truncated || !strings.Contains(page.Text, "0 entries") {
		t.Fatalf("past EOF %+v %v", page, e)
	}
	if e = testfs.Symlink("internal/tui", filepath.Join(root, "alias")); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"alias", "../escape"} {
		if _, e = call("read", map[string]any{"path": path}); e == nil {
			t.Fatal("unsafe directory accepted", path)
		}
	}
}

func TestDirectoryReadByteBound(t *testing.T) {
	root := t.TempDir()
	name := strings.Repeat("x", 180)
	if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
	caps := config.Defaults().Tools.Bash
	caps.MaxBytes = 256
	w := &Workspace{Path: root, Caps: caps}
	if result, err := invoke(t, w, "read", map[string]string{"path": "."}); err == nil || result.Changed || !strings.Contains(err.Error(), "max_bytes") {
		t.Fatalf("one entry must refuse, not disappear: %+v %v", result, err)
	}
}

func TestImmediateDirectoryFinderAndFileCompatibility(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"empty", ".hidden", "ignored", "parent/nested"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parent", "fixture.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := testfs.Symlink("empty", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	owner, ok := any(w).(interface{ FindDirDefinition() tool.Definition })
	if !ok {
		t.Fatal("directory finder unavailable")
	}
	d := owner.FindDirDefinition()
	call := func(args string) (tool.Result, error) {
		t.Helper()
		a, refusal := tool.ValidateJSON(d, args)
		if refusal != "" {
			t.Fatal(refusal)
		}
		return d.Execute(t.Context(), a)
	}
	seen := map[string]bool{}
	for offset := 1; offset <= 4; offset++ {
		res, err := call(fmt.Sprintf(`{"limit":1,"offset":%d}`, offset))
		if err != nil || res.Changed || res.FileEffect != nil || strings.Contains(res.Text, "sha256") {
			t.Fatalf("%+v %v", res, err)
		}
		for _, row := range strings.Split(res.Text, "\n") {
			if !strings.HasPrefix(row, "dir ") {
				continue
			}
			var path string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(row, "dir ")), &path); err != nil {
				t.Fatal(err)
			}
			if seen[path] {
				t.Fatalf("duplicate %q", path)
			}
			seen[path] = true
		}
		if res.Truncated != (offset < 4) {
			t.Fatal(res.Text)
		}
	}
	if len(seen) != 4 || !seen["empty"] || !seen[".hidden"] || !seen["ignored"] || !seen["parent"] {
		t.Fatal(seen)
	}
	res, err := call(`{"pattern":"*hidden*"}`)
	if err != nil || !strings.Contains(res.Text, `dir ".hidden"`) || res.Truncated {
		t.Fatalf("%+v %v", res, err)
	}
	for _, args := range []string{`{"path":"link"}`, `{"path":"../outside"}`, `{"pattern":"parent/*"}`, `{"pattern":"["}`, `{"offset":0}`, `{"limit":0}`} {
		if res, err := call(args); err == nil || res.Changed {
			t.Fatalf("invalid accepted %s %+v %v", args, res, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a, _ := tool.ValidateJSON(d, `{}`)
	if _, err := d.Execute(ctx, a); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	legacy := search.NewFindAt(config.Defaults().Tools.Search, root)
	file := search.NewFindFileAt(config.Defaults().Tools.Search, root)
	if legacy.Name != "find" || file.Name != "find_file" {
		t.Fatal(legacy.Name, file.Name)
	}
	for _, def := range []tool.Definition{legacy, file} {
		a, refusal := tool.ValidateJSON(def, `{"pattern":"*.go","path":"parent"}`)
		if refusal != "" {
			t.Fatal(refusal)
		}
		res, err := def.Execute(t.Context(), a)
		if err != nil || res.Text != "parent/fixture.go" {
			t.Fatalf("%+v %v", res, err)
		}
	}
}

func TestDirectoryFinderSelectionBasis(t *testing.T) {
	root := t.TempDir()
	path := "files only é"
	if err := os.Mkdir(filepath.Join(root, path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path, "source.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
	reg := tool.NewRegistry()
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
	reg.Register(w.FindDirDefinition())
	call := func(name string, slots map[string]any) (tool.Result, error) {
		t.Helper()
		d, _ := reg.Lookup(name)
		raw, _ := json.Marshal(slots)
		a, refusal := tool.ValidateJSON(d, string(raw))
		if refusal != "" {
			t.Fatal(refusal)
		}
		return d.Execute(t.Context(), a)
	}
	read, err := call("read", map[string]any{"path": path})
	quotedFile, _ := json.Marshal(filepath.Join(path, "source.go"))
	want := fmt.Sprintf("Directory %q (live directory page; names/order may shift; no file hash or source coverage)\nfile %s\n[1 entries shown from entry 1; EOF=true; truncated=false]", path, quotedFile)
	if err != nil || read.Text != want || read.FileEffect != nil || read.Changed {
		t.Fatalf("ordinary Read changed %+v %v", read, err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := call("read", map[string]any{"path": "empty.txt"})
	if err != nil || empty.NoMatches || empty.Failed || empty.Blocked || !strings.Contains(empty.Text, "sha256=") {
		t.Fatalf("empty file is a successful source observation: %+v %v", empty, err)
	}
	zero, err := call("find_dir", map[string]any{"path": path, "pattern": "absent*"})
	quotedPath, _ := json.Marshal(path)
	if err != nil || !zero.NoMatches || zero.Changed || zero.FileEffect != nil || zero.Truncated || strings.Contains(zero.Text, "sha256") || !strings.Contains(zero.Text, `matching "absent*"`) || !strings.Contains(zero.Text, "files excluded") || !strings.Contains(zero.Text, "read(path="+string(quotedPath)+")") || !strings.Contains(zero.Text, "0 entries shown") {
		t.Fatalf("selected zero must not imply no files: %+v %v", zero, err)
	}
	for _, name := range []string{"match-a", "match-b", "other"} {
		if err := os.Mkdir(filepath.Join(root, path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for off := 1; off <= 2; off++ {
		res, e := call("find_dir", map[string]any{"path": path, "pattern": "match-*", "offset": off, "limit": 1})
		if e != nil || res.Changed || res.FileEffect != nil || len(res.Text) > w.Caps.MaxBytes || res.Truncated != (off == 1) || !strings.Contains(res.Text, `matching "match-*"`) || strings.Contains(res.Text, "source.go") || strings.Contains(res.Text, `dir "files only é/other"`) {
			t.Fatalf("filtered page %+v %v", res, e)
		}
		for _, row := range strings.Split(res.Text, "\n") {
			if strings.HasPrefix(row, "dir ") {
				var actual string
				if e := json.Unmarshal([]byte(strings.TrimPrefix(row, "dir ")), &actual); e != nil {
					t.Fatal(e)
				}
				if seen[actual] {
					t.Fatal("duplicate page", actual)
				}
				seen[actual] = true
			}
		}
		if off == 1 && !strings.Contains(res.Text, "next_offset=2") || off == 2 && strings.Contains(res.Text, "next_offset=") {
			t.Fatal(res.Text)
		}
	}
	if len(seen) != 2 || !seen[filepath.Join(path, "match-a")] || !seen[filepath.Join(path, "match-b")] {
		t.Fatal(seen)
	}
	w.Caps.MaxBytes = len(zero.Text)
	exact, err := call("find_dir", map[string]any{"path": path, "pattern": "absent*"})
	if err != nil || exact.Text != zero.Text {
		t.Fatalf("exact metadata cap %+v %v", exact, err)
	}
	w.Caps.MaxBytes--
	failed, err := call("find_dir", map[string]any{"path": path, "pattern": "absent*"})
	if err == nil || !strings.Contains(err.Error(), "max_bytes") || failed.Changed || failed.FileEffect != nil {
		t.Fatalf("metadata overflow hidden %+v %v", failed, err)
	}
}
