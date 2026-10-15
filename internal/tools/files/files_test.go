package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"sync"
	"testing"
)

func invoke(t *testing.T, w *Workspace, name string, slots map[string]string) (tool.Result, error) {
	t.Helper()
	for _, d := range w.Definitions() {
		if d.Name == name {
			a, q := tool.Validate(d, slots)
			if q != "" {
				t.Fatal(q)
			}
			return d.Execute(context.Background(), a)
		}
	}
	t.Fatal(name)
	return tool.Result{}, nil
}
func TestWorkspaceExactBytesDeletionAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	raw := "café\r\n\tkeep\r\n"
	if e := os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dir, "a"), 0666); e != nil {
		t.Fatal(e)
	}
	read, e := invoke(t, w, "read", map[string]string{"path": "a", "limit": "1"})
	if e != nil || !strings.Contains(read.Text, "\ncafé\r\n") || !strings.Contains(read.Text, digest([]byte(raw))) || !read.Truncated {
		t.Fatalf("read %+v %v", read, e)
	}
	res, e := invoke(t, w, "edit", map[string]string{"path": "a", "old": "café\r\n", "new": "", "expected_hash": digest([]byte(raw))})
	if e != nil || !res.Changed {
		t.Fatalf("delete %+v %v", res, e)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a"))
	if string(got) != "\tkeep\r\n" {
		t.Fatalf("bytes %q", got)
	}
	info, _ := os.Stat(filepath.Join(dir, "a"))
	if info.Mode().Perm() != 0666 {
		t.Fatal("permissions changed")
	}
	for _, d := range w.Definitions() {
		if d.Name == "edit" {
			if _, q := tool.ValidateJSON(d, `{"path":"a","old":"keep","expected_hash":"x"}`); q == "" {
				t.Fatal("missing new must not silently delete")
			}
		}
	}
	res, e = invoke(t, w, "write", map[string]string{"path": "empty", "content": ""})
	if e != nil || !res.Changed {
		t.Fatalf("empty %+v %v", res, e)
	}
	if _, e = invoke(t, w, "write", map[string]string{"path": "empty", "content": "again"}); e == nil {
		t.Fatal("create overwrote existing")
	}
	res, e = invoke(t, w, "write", map[string]string{"path": "a", "content": "", "expected_hash": digest(got)})
	if e != nil || !res.Changed {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a")); len(b) != 0 {
		t.Fatal("empty replacement changed")
	}
	if temps, _ := filepath.Glob(filepath.Join(dir, ".specific-*")); len(temps) != 0 {
		t.Fatalf("owned temp leak %v", temps)
	}
}
func TestWorkspaceBoundsAndOneWinningEditor(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	os.WriteFile(filepath.Join(dir, "a"), []byte("original"), 0644)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, next := range []string{"first", "second"} {
		wg.Add(1)
		go func(next string) {
			defer wg.Done()
			for _, d := range w.Definitions() {
				if d.Name == "edit" {
					_, e := d.Execute(context.Background(), tool.Args{"path": "a", "old": "original", "new": next, "expected_hash": digest([]byte("original"))})
					results <- e
				}
			}
		}(next)
	}
	wg.Wait()
	wins := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("winners %d", wins)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("safe"), 0644)
	for _, path := range []string{"../escape", outside} {
		if _, e := invoke(t, w, "read", map[string]string{"path": path}); e == nil {
			t.Fatal("unsafe path", path)
		}
	}
	os.Symlink(outside, filepath.Join(dir, "link"))
	if _, e := invoke(t, w, "write", map[string]string{"path": "link", "content": "bad", "expected_hash": digest([]byte("safe"))}); e == nil {
		t.Fatal("symlink accepted")
	}
	os.WriteFile(filepath.Join(dir, "big"), []byte(strings.Repeat("x", w.Caps.MaxBytes+1)), 0644)
	if page, e := invoke(t, w, "read", map[string]string{"path": "big"}); e != nil || !page.Truncated || len(page.Text) > w.Caps.MaxBytes || !strings.Contains(page.Text, "shown_range=none") {
		t.Fatalf("oversized source line returned: %+v %v", page, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, d := range w.Definitions() {
		if d.Name == "write" {
			res, e := d.Execute(ctx, tool.Args{"path": "cancelled", "content": "no"})
			if e == nil || res.Changed {
				t.Fatalf("cancel %+v %v", res, e)
			}
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "cancelled")); !os.IsNotExist(e) {
		t.Fatal("cancel effect")
	}
}

func TestWorkspaceNativeEmptyContentUsesActualExec(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	os.WriteFile(filepath.Join(dir, "a"), []byte("delete"), 0600)
	for _, c := range []struct {
		name  string
		slots map[string]any
	}{
		{"edit", map[string]any{"path": "a", "old": "delete", "new": "", "expected_hash": digest([]byte("delete"))}},
		{"write", map[string]any{"path": "empty", "content": ""}},
	} {
		for _, d := range w.Definitions() {
			if d.Name != c.name {
				continue
			}
			missing := map[string]any{}
			for k, v := range c.slots {
				missing[k] = v
			}
			if c.name == "edit" {
				delete(missing, "new")
			} else {
				delete(missing, "content")
			}
			raw, _ := json.Marshal(missing)
			if _, q := tool.ValidateJSON(d, string(raw)); q == "" {
				t.Fatal("missing content must refuse")
			}
			raw, _ = json.Marshal(c.slots)
			a, q := tool.ValidateJSON(d, string(raw))
			if q != "" {
				t.Fatal(q)
			}
			res, e := agent.Direct(context.Background(), d, a)
			if e != nil || !res.Changed {
				t.Fatalf("actual native exec %+v %v", res, e)
			}
		}
	}
	for _, name := range []string{"a", "empty"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || len(b) != 0 {
			t.Fatalf("bytes %q %v", b, e)
		}
	}
}

func TestWorkspaceReadReturnsSelectedOriginalBytes(t *testing.T) {
	const source = "header\r\n\t```tool \\t  \r\n\r\nlast  "
	for _, tc := range []struct {
		name, input, offset, limit, selected string
		truncated                            bool
	}{
		{"no-final-newline", source, "1", "500", source, false},
		{"final-newline", source + "\r\n", "1", "500", source + "\r\n", false},
		{"page", source, "2", "2", "\t```tool \\t  \r\n\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			if e := os.WriteFile(filepath.Join(dir, "source"), []byte(tc.input), 0600); e != nil {
				t.Fatal(e)
			}
			res, e := invoke(t, w, "read", map[string]string{"path": "source", "offset": tc.offset, "limit": tc.limit})
			if e != nil {
				t.Fatal(e)
			}
			header := "source sha256=" + digest([]byte(tc.input)) + "\n"
			end := strings.LastIndex(res.Text, "\n[")
			if !strings.HasPrefix(res.Text, header) || end < len(header) {
				t.Fatalf("metadata missing: %q", res.Text)
			}
			if got := res.Text[len(header):end]; got != tc.selected {
				t.Fatalf("selected source changed: got %q, want %q", got, tc.selected)
			}
			if res.Truncated != tc.truncated || !strings.Contains(res.Text[end:], "EOF="+map[bool]string{true: "false", false: "true"}[tc.truncated]) {
				t.Fatalf("untruthful tail: %+v", res)
			}
			if got, e := os.ReadFile(filepath.Join(dir, "source")); e != nil || string(got) != tc.input || res.Changed {
				t.Fatalf("read mutated source: %q %v", got, e)
			}
		})
	}
}

func TestStreamedReadLargeSourceWindow(t *testing.T) {
	dir := t.TempDir()
	var source strings.Builder
	source.WriteString("package handlers\n\n")
	for i := 0; i < 1400; i++ {
		fmt.Fprintf(&source, "func Handler%04d() string { return \"GET /resource/%04d — handler source\" }\n", i, i)
	}
	raw := source.String()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	if len(raw) <= w.Caps.MaxBytes {
		t.Fatal("fixture must exceed result cap")
	}
	p := filepath.Join(dir, "handlers.go")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	for _, d := range w.Definitions() {
		registry.Register(d)
	}
	d, _ := registry.Lookup("read")
	args, repair := tool.ValidateJSON(d, `{"path":"handlers.go","offset":777,"limit":2}`)
	if repair != "" {
		t.Fatal(repair)
	}
	page, err := d.Execute(context.Background(), args)
	want := "handlers.go sha256=" + digest([]byte(raw)) + "\n" + strings.Join(sourceLines(raw)[776:778], "")
	if err != nil || !strings.HasPrefix(page.Text, want) || !page.Truncated || page.Changed || len(page.Text) > w.Caps.MaxBytes || !strings.Contains(page.Text, "shown_range=777-778") || !strings.Contains(page.Text, "next_offset=779") {
		t.Fatalf("narrow large source: %+v %v", page, err)
	}
	// A bounded read does not impose an output-sized whole-file mutation ceiling.
	for _, name := range []string{"edit", "write"} {
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		slots := map[string]string{"path": "handlers.go", "expected_hash": digest([]byte(raw))}
		want := "replacement\n"
		if name == "edit" {
			slots["old"], slots["new"] = sourceLines(raw)[776], "replacement\n"
			want = strings.Replace(raw, sourceLines(raw)[776], "replacement\n", 1)
		} else {
			slots["content"] = want
		}
		res, err := invoke(t, w, name, slots)
		got, readErr := os.ReadFile(p)
		if err != nil || !res.Changed || readErr != nil || string(got) != want || len(res.Text) > w.Caps.MaxBytes {
			t.Fatalf("large %s: %+v %v", name, res, err)
		}
	}

}

// A real cancellation is triggered at a deterministic scan checkpoint.
type scanCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *scanCancelContext) Err() error {
	c.checks++
	if c.checks == 8 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestStreamedReadFullValidationAndCancellation(t *testing.T) {
	prefix := "visible\n" + strings.Repeat("x", 100000) + "\n"
	for _, bad := range []string{"\x00", "\xff", "\xe2\x82"} {
		t.Run(fmt.Sprintf("invalid_%x", bad), func(t *testing.T) {
			dir := t.TempDir()
			w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			raw := prefix + bad
			os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600)
			res, err := invoke(t, w, "read", map[string]string{"path": "a", "limit": "1"})
			if err == nil || !strings.Contains(err.Error(), "binary or invalid UTF-8") || res.Text != "" || len(w.publications) != 0 {
				t.Fatalf("unshown invalid suffix admitted: %+v %v", res, err)
			}
		})
	}
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	os.WriteFile(filepath.Join(dir, "a"), []byte(prefix), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &scanCancelContext{Context: ctx, cancel: cancel}
	for _, d := range w.Definitions() {
		if d.Name != "read" {
			continue
		}
		res, err := d.Execute(observed, tool.Args{"path": "a", "offset": int64(1), "limit": int64(1)})
		if !errors.Is(err, context.Canceled) || res.Text != "" || len(w.publications) != 0 || observed.checks != 8 {
			t.Fatalf("cancelled scan granted evidence: %+v %v checks=%d", res, err, observed.checks)
		}
	}
}

func TestStreamedReadRawLinesAndOversizedLine(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	// The multibyte rune crosses the reader's 4096-byte fragment boundary.
	raw := strings.Repeat("x", 4095) + "é\r\n" + strings.Repeat("z", 100000) + "\nlast Δ"
	os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600)
	defs := w.Definitions()
	for _, tc := range []struct {
		offset, limit, body, span, cursor string
		more                              bool
	}{
		{"1", "1", sourceLines(raw)[0], "1-1", "next_offset=2", true},
		{"2", "1", "", "none", "", true},
		{"3", "1", "last Δ", "3-3", "", false},
		{"4", "1", "", "none", "", false},
	} {
		page, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": tc.offset, "limit": tc.limit})
		want := "a sha256=" + digest([]byte(raw)) + "\n" + tc.body + "\n["
		if err != nil || !strings.HasPrefix(page.Text, want) || page.Truncated != tc.more || len(page.Text) > w.Caps.MaxBytes || !strings.Contains(page.Text, "shown_range="+tc.span) || tc.cursor != "" && !strings.Contains(page.Text, tc.cursor) {
			t.Fatalf("offset %s raw page: %+v %v", tc.offset, page, err)
		}
	}
	// Existing regular-file/symlink rules apply to the streaming branch too.
	os.Symlink("a", filepath.Join(dir, "link"))
	if res, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "link"}); err == nil || res.Text != "" {
		t.Fatalf("final symlink read: %+v %v", res, err)
	}
}
