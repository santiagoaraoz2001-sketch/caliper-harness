package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

const navigationSource = "package example\n\n// Item documentation.\ntype Item struct { Value string }\n\n// Pick documentation.\nfunc (i *Item) Pick(\n value string,\n) string {\n return `brace } {` + value\n}\n\ntype Other struct{}\nfunc (Other) Pick() {}\nvar First, Second = 1, 2\nconst (\n Zero = iota\n One\n)\nfunc TestHelper() {}\n"

func navigationFixture(t *testing.T) (*Workspace, []tool.Definition) {
	t.Helper()
	w := &Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	if err := os.WriteFile(filepath.Join(w.Path, "source.go"), []byte(navigationSource), 0600); err != nil {
		t.Fatal(err)
	}
	return w, w.Definitions()
}

func navigationCall(t *testing.T, defs []tool.Definition, name string, args map[string]any) (tool.Result, error) {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			raw, _ := json.Marshal(args)
			a, repair := tool.ValidateJSON(d, string(raw))
			if repair != "" {
				return tool.Result{}, fmt.Errorf("schema: %s", repair)
			}
			return d.Execute(context.Background(), a)
		}
	}
	return tool.Result{}, fmt.Errorf("missing %s", name)
}

func TestReadDefinitionExactSpan(t *testing.T) {
	w, defs := navigationFixture(t)
	r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "definition": "Item.Pick"})
	want := "// Pick documentation.\nfunc (i *Item) Pick(\n value string,\n) string {\n return `brace } {` + value\n}\n"
	if err != nil || !strings.Contains(r.Text, "\n"+want) || strings.Contains(r.Text, "type Other") || r.Truncated || !strings.Contains(r.Text, "selection_complete=true") || !strings.Contains(r.Text, "EOF=false") || !strings.Contains(r.Text, "sha256="+digest([]byte(navigationSource))) {
		t.Fatalf("definition must return the exact complete declaration, not the file: %+v %v", r, err)
	}
	if len(w.publications) != 1 {
		t.Fatal("definition source was not recorded")
	}
}

func TestReadOutlineNavigationOnly(t *testing.T) {
	w, defs := navigationFixture(t)
	r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "outline": true})
	if err != nil || !strings.Contains(r.Text, "Item.Pick") || !strings.Contains(r.Text, "Other.Pick") || !strings.Contains(r.Text, "TestHelper") || !strings.Contains(r.Text, "no source coverage") || strings.Contains(r.Text, "return `brace") || len(w.publications) != 0 {
		t.Fatalf("outline must list declarations without source authority: %+v %v", r, err)
	}
}

func TestReadNavigationDeclarationsAndFailures(t *testing.T) {
	w, defs := navigationFixture(t)
	for _, tc := range []struct{ name, source string }{
		{"Item", "// Item documentation.\ntype Item struct { Value string }\n"},
		{"First", "var First, Second = 1, 2\n"},
		{"Second", "var First, Second = 1, 2\n"},
		{"One", "const (\n Zero = iota\n One\n)\n"},
		{"TestHelper", "func TestHelper() {}\n"},
	} {
		r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "definition": tc.name})
		if err != nil || !strings.Contains(r.Text, "\n"+tc.source) || r.Truncated {
			t.Errorf("%s: %+v %v", tc.name, r, err)
		}
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"definition": "Pick"}, "ambiguous"},
		{map[string]any{"definition": "ask.Answer"}, "no Go declaration"},
		{map[string]any{"definition": "Item.Pick", "outline": true}, "not both"},
		{map[string]any{"definition": " "}, "nonempty"},
		{map[string]any{"definition": "Item.Pick", "offset": 6}, "offset=1"},
		{map[string]any{"outline": true, "limit": 2001}, "1..2000"},
		{map[string]any{"outline": true, "offset": 0}, ">=1"},
	} {
		tc.args["path"] = "source.go"
		r, err := navigationCall(t, defs, "read", tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) || r.Text != "" {
			t.Errorf("%v: %+v %v", tc.args, r, err)
		}
	}
	for _, tc := range []struct{ path, raw, want string }{
		{"source.py", "def function():\n  pass\n", "supports Go syntax"},
		{"invalid.go", "package p\nfunc broken(\n", "parse failed"},
		{"binary.go", "package p\nvar X = \"\x00\"\n", "binary or invalid UTF-8"},
		{"utf8.go", "package p\n//\xff", "binary or invalid UTF-8"},
	} {
		os.WriteFile(filepath.Join(w.Path, tc.path), []byte(tc.raw), 0600)
		r, err := navigationCall(t, defs, "read", map[string]any{"path": tc.path, "outline": true})
		if err == nil || !strings.Contains(err.Error(), tc.want) || r.Text != "" {
			t.Errorf("%s: %+v %v", tc.path, r, err)
		}
	}
}

func TestReadNavigationCoverageAndPages(t *testing.T) {
	w, defs := navigationFixture(t)
	r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "definition": "Item.Pick", "limit": 2})
	if err != nil || !r.Truncated || !strings.Contains(r.Text, "shown_range=6-7") || !strings.Contains(r.Text, "next_offset=8") || !strings.Contains(r.Text, "selection_complete=false") {
		t.Fatalf("definition page: %+v %v", r, err)
	}
	// Unreturned body lines must remain uneditable, even with the full-file hash.
	edit := map[string]any{"path": "source.go", "start_line": 8, "end_line": 8, "new": " x string,\n", "expected_hash": digest([]byte(navigationSource))}
	if _, err := navigationCall(t, defs, "edit", edit); err == nil || !strings.Contains(err.Error(), "not returned by current read") {
		t.Fatalf("unreturned definition body admitted: %v", err)
	}
	if _, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "offset": 8, "limit": 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := navigationCall(t, defs, "edit", edit); err != nil {
		t.Fatalf("actual continued source did not grant coverage: %v", err)
	}
	w, defs = navigationFixture(t)
	var names []string
	for offset := 1; offset <= 9; offset++ {
		r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "outline": true, "offset": offset, "limit": 1})
		if err != nil || !strings.Contains(r.Text, "1 declarations shown") || r.Truncated != (offset < 9) {
			t.Fatalf("outline page%d: %+v %v", offset, r, err)
		}
		for _, line := range strings.Split(r.Text, "\n") {
			if strings.Contains(line, " definition=") {
				names = append(names, line)
			}
		}
	}
	if len(names) != 9 || len(w.publications) != 0 {
		t.Fatalf("outline invented coverage or lost names: %v", names)
	}
	r, err = navigationCall(t, defs, "read", map[string]any{"path": "source.go", "outline": true, "offset": 100})
	if err != nil || r.Truncated || !strings.Contains(r.Text, "0 declarations shown") {
		t.Fatalf("beyond outline: %+v %v", r, err)
	}
	if _, err := navigationCall(t, defs, "edit", edit); err == nil {
		t.Fatal("outline metadata granted edit coverage")
	}
}

func TestReadNavigationPhysicalLocationsAndSnapshot(t *testing.T) {
	w, defs := navigationFixture(t)
	raw := "package p\r\n//line imaginary.go:500\r\n// Generic docs.\r\ntype Box[T any] struct{}\r\nfunc (b *Box[T]) Method() { _ = `}\r\n{` }\r\n"
	os.WriteFile(filepath.Join(w.Path, "source.go"), []byte(raw), 0600)
	r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "definition": "Box.Method"})
	if err != nil || !strings.Contains(r.Text, "declaration_range=5-6") || !strings.Contains(r.Text, "\nfunc (b *Box[T]) Method() { _ = `}\r\n{` }\r\n") || !strings.Contains(r.Text, "EOF=true") {
		t.Fatalf("physical CRLF ranges: %+v %v", r, err)
	}
	// Replace the underlying file after snapshot capture, at the first parser
	// checkpoint. Returned source/hash must still describe the same old bytes.
	os.WriteFile(filepath.Join(w.Path, "source.go"), []byte(navigationSource), 0600)
	ctx := &navigationMutationContext{Context: context.Background(), mutate: func() {
		os.WriteFile(filepath.Join(w.Path, "source.go"), []byte("package p\nfunc Different() {}\n"), 0600)
	}}
	d := defs[0]
	a, repair := tool.ValidateJSON(d, `{"path":"source.go","definition":"Item.Pick"}`)
	if repair != "" {
		t.Fatal(repair)
	}
	r, err = d.Execute(ctx, a)
	if err != nil || !strings.Contains(r.Text, "sha256="+digest([]byte(navigationSource))) || !strings.Contains(r.Text, "return `brace } {`") || strings.Contains(r.Text, "Different") {
		t.Fatalf("mixed snapshot: %+v %v", r, err)
	}
	edit := map[string]any{"path": "source.go", "old": "return `brace } {` + value", "new": "return value", "expected_hash": digest([]byte(navigationSource))}
	if _, err := navigationCall(t, defs, "edit", edit); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("stale snapshot did not refuse at hash guard: %v", err)
	}
}

type navigationMutationContext struct {
	context.Context
	checks int
	mutate func()
}

func (c *navigationMutationContext) Err() error {
	c.checks++
	// execute, first Read, EOF Read, snapshot completion, then AST walk.
	if c.checks == 5 {
		c.mutate()
	}
	return c.Context.Err()
}

func TestReadNavigationCapsScopeAndCancel(t *testing.T) {
	w, defs := navigationFixture(t)
	large := "package p\n// " + strings.Repeat("x", w.Caps.MaxBytes) + "\nfunc X() {}\n"
	os.WriteFile(filepath.Join(w.Path, "large.go"), []byte(large), 0600)
	for _, selector := range []map[string]any{{"definition": "X"}, {"outline": true}} {
		selector["path"] = "large.go"
		if res, err := navigationCall(t, defs, "read", selector); err != nil || len(res.Text) > w.Caps.MaxBytes {
			t.Fatalf("large source with bounded selector output: %+v %v", res, err)
		}
	}
	if r, err := navigationCall(t, defs, "read", map[string]any{"path": "large.go", "limit": 1}); err != nil || !strings.Contains(r.Text, "package p") {
		t.Fatalf("ordinary streaming read regressed: %+v %v", r, err)
	}
	outside := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(outside, []byte(navigationSource), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".", "../outside.go", outside} {
		if _, err := navigationCall(t, defs, "read", map[string]any{"path": path, "outline": true}); err == nil {
			t.Fatal("unsafe/nonfile selector accepted", path)
		}
	}
	os.Symlink(filepath.Join(w.Path, "source.go"), filepath.Join(w.Path, "link.go"))
	if _, err := navigationCall(t, defs, "read", map[string]any{"path": "link.go", "outline": true}); err == nil {
		t.Fatal("final symlink admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := defs[0].Execute(ctx, tool.Args{"path": "source.go", "outline": true, "offset": int64(1), "limit": int64(2000)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	w, defs = navigationFixture(t)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	observed := &scanCancelContext{Context: ctx, cancel: cancel}
	r, err := defs[0].Execute(observed, tool.Args{"path": "source.go", "outline": true, "offset": int64(1), "limit": int64(2000)})
	if !errors.Is(err, context.Canceled) || r.Text != "" || len(w.publications) != 0 {
		t.Fatalf("cancel after snapshot/parse published evidence: %+v %v", r, err)
	}
	w.Caps.MaxBytes = 400 // Snapshot fits; output metadata makes the declaration page partial.
	r, err = navigationCall(t, defs, "read", map[string]any{"path": "source.go", "definition": "Item.Pick"})
	if err != nil || len(r.Text) > 400 || !r.Truncated {
		t.Fatalf("bounded declaration output: %+v %v", r, err)
	}
	r, err = navigationCall(t, defs, "read", map[string]any{"path": "source.go", "outline": true})
	if err != nil || len(r.Text) > 400 || !r.Truncated {
		t.Fatalf("bounded outline output: %+v %v", r, err)
	}
}

func TestReadNavigationOutsideParity(t *testing.T) {
	w, _ := navigationFixture(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outside, "source.go")
	os.WriteFile(path, []byte(navigationSource), 0600)
	defs := w.OutsideDefinitions(config.Defaults().Tools.Search)
	r, err := navigationCall(t, defs, "read_outside", map[string]any{"path": path, "definition": "Item.Pick"})
	if err != nil || !strings.HasPrefix(r.Text, path+" sha256=") || !strings.Contains(r.Text, "selection_complete=true") || len(w.publications) != 0 {
		t.Fatalf("outside source/hash/coverage: %+v %v", r, err)
	}
	r, err = navigationCall(t, defs, "read_outside", map[string]any{"path": path, "outline": true})
	if err != nil || !strings.Contains(r.Text, "Item.Pick") || len(w.publications) != 0 {
		t.Fatalf("outside outline: %+v %v", r, err)
	}
}

func TestReadOutlineEmptyPageIsNotDiscovery(t *testing.T) {
	w, defs := navigationFixture(t)
	os.WriteFile(filepath.Join(w.Path, "empty.go"), []byte("package empty\n"), 0600)
	for _, args := range []map[string]any{
		{"path": "empty.go", "outline": true},
		{"path": "source.go", "outline": true, "offset": 100},
	} {
		r, err := navigationCall(t, defs, "read", args)
		if err != nil || !r.NoMatches || !strings.Contains(r.Text, "0 declarations shown") || len(w.publications) != 0 {
			t.Fatalf("empty navigation page must not count as fresh discovery: %+v %v", r, err)
		}
	}
	r, err := navigationCall(t, defs, "read", map[string]any{"path": "source.go", "outline": true, "limit": 1})
	if err != nil || r.NoMatches || !r.Truncated {
		t.Fatalf("nonempty page must remain discovery: %+v %v", r, err)
	}
}
