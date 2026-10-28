package files

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"sync"
	"testing"
)

func lineInvoke(defs []tool.Definition, native bool, slots map[string]string) (tool.Result, error) {
	reg := tool.NewRegistry()
	for _, d := range defs {
		reg.Register(d)
	}
	d, _ := reg.Lookup("edit")
	var a tool.Args
	var q string
	if native {
		v := map[string]any{}
		for k, x := range slots {
			if k == "start_line" || k == "end_line" {
				v[k] = json.Number(x)
			} else {
				v[k] = x
			}
		}
		b, _ := json.Marshal(v)
		a, q = tool.ValidateJSON(d, string(b))
	} else {
		a, q = tool.Validate(d, slots)
	}
	if q != "" {
		return tool.Result{}, fmtError(q)
	}
	return d.Execute(context.Background(), a)
}

type fmtError string

func (e fmtError) Error() string { return string(e) }

// A whole-line edit must not silently fuse its last replacement line with a
// caller's untouched code. Exact spans and the end of the file remain literal.
func TestLineEditBoundaryRefusesBeforePublication(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("native=%t/newline=%q", native, newline), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "a")
				raw := "head" + newline + "old" + newline + "tail"
				if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
				w := &Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
				defs := w.Definitions()
				if _, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "a"}); err != nil {
					t.Fatal(err)
				}
				args := map[string]string{"path": "a", "start_line": "2", "end_line": "2", "new": "replacement", "expected_hash": digest([]byte(raw))}
				res, err := lineInvoke(defs, native, args)
				if err == nil || !strings.Contains(err.Error(), "untouched line 3") || res.Changed || res.FileEffect != nil {
					t.Fatalf("ambiguous edit published: %+v %v", res, err)
				}
				unchanged, err := os.ReadFile(path)
				if err != nil || string(unchanged) != raw {
					t.Fatalf("refusal changed bytes: %q %v", unchanged, err)
				}
				// Correction uses the same observation/hash: no forced reread.
				args["new"] += newline
				if res, err = lineInvoke(defs, native, args); err != nil || !res.Changed {
					t.Fatalf("corrected edit: %+v %v", res, err)
				}
				want := "head" + newline + "replacement" + newline + "tail"
				got, _ := os.ReadFile(path)
				if string(got) != want {
					t.Fatalf("untouched boundary: %q", got)
				}
				// EOF does not need a separator and must not acquire one.
				if _, err = invokeDefinitions(t, defs, "read", map[string]string{"path": "a"}); err != nil {
					t.Fatal(err)
				}
				args["expected_hash"], args["start_line"], args["end_line"], args["new"] = digest(got), "3", "3", "end"
				if _, err = lineInvoke(defs, native, args); err != nil {
					t.Fatal(err)
				}
				// An explicit exact-span join is intentional and stays possible.
				if _, err = lineInvoke(defs, native, map[string]string{"path": "a", "old": "replacement" + newline, "new": "joined"}); err != nil {
					t.Fatal(err)
				}
				got, _ = os.ReadFile(path)
				if string(got) != "head"+newline+"joinedend" {
					t.Fatalf("literal exact span/EOF changed: %q", got)
				}
			})
		}
	}
}

func TestLineEditInterfaceBytesAndGuards(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "native"}[native], func(t *testing.T) {
			dir := t.TempDir()
			w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			defs := w.Definitions()
			p := filepath.Join(dir, "a")
			raw := "prefix\r\n\told  \r\n\r\né末"
			os.WriteFile(p, []byte(raw), 0600)
			os.Chmod(p, 0666)
			for _, slots := range []map[string]string{
				{"new": "x"}, {"start_line": "2", "new": "x"}, {"end_line": "3", "new": "x"}, {"start_line": "-1", "end_line": "2", "new": "x"}, {"start_line": "0", "end_line": "2", "new": "x"}, {"start_line": "3", "end_line": "2", "new": "x"}, {"start_line": "1", "end_line": "5", "new": "x"}, {"start_line": "9223372036854775807", "end_line": "9223372036854775807", "new": "x"}, {"start_line": "9223372036854775808", "end_line": "2", "new": "x"}, {"old": "old", "start_line": "0", "end_line": "0", "new": "x"}, {"old": "old", "start_line": "2", "end_line": "2", "new": "x"}, {"start_line": "2", "end_line": "2"},
			} {
				slots["path"] = "a"
				slots["expected_hash"] = digest([]byte(raw))
				r, e := lineInvoke(defs, native, slots)
				if e == nil || r.Changed {
					t.Fatalf("invalid published %v %+v %v", slots, r, e)
				}
				b, _ := os.ReadFile(p)
				if string(b) != raw {
					t.Fatal("invalid effect")
				}
			}
			if _, e := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": "2", "limit": "2"}); e != nil {
				t.Fatal(e)
			}
			slots := map[string]string{"path": "a", "start_line": "2", "end_line": "3", "new": "\tnew\r\n", "expected_hash": digest([]byte(raw))}
			r, e := lineInvoke(defs, native, slots)
			if e != nil || !r.Changed {
				t.Fatalf("range refused %+v %v", r, e)
			}
			want := "prefix\r\n\tnew\r\né末"
			b, _ := os.ReadFile(p)
			if string(b) != want {
				t.Fatalf("bytes %q", b)
			}
			i, _ := os.Stat(p)
			if i.Mode().Perm() != 0666 {
				t.Fatal("permissions")
			}
			if r, e = lineInvoke(defs, native, slots); e == nil || r.Changed {
				t.Fatal("stale accepted")
			}
			if _, e := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": "2", "limit": "1"}); e != nil {
				t.Fatal(e)
			}
			slots["expected_hash"] = digest(b)
			slots["end_line"] = "2"
			r, e = lineInvoke(defs, native, slots)
			if e != nil || r.Changed || r.FileEffect != nil {
				t.Fatalf("noop %+v %v", r, e)
			}
			slots["new"] = ""
			r, e = lineInvoke(defs, native, slots)
			if e != nil || !r.Changed {
				t.Fatalf("delete %+v %v", r, e)
			}
			b, _ = os.ReadFile(p)
			if string(b) != "prefix\r\né末" {
				t.Fatalf("delete bytes %q", b)
			}
			_, e = lineInvoke(defs, native, map[string]string{"path": "a", "old": "prefix", "new": "head", "expected_hash": digest(b)})
			if e != nil {
				t.Fatalf("old compatibility %v", e)
			}
		})
	}
}
func TestLineEditInterfaceOneWinner(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte("a\nb\n"), 0600)
	if _, e := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": "2", "limit": "1"}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, n := range []string{"x\n", "y\n"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			_, e := lineInvoke(defs, true, map[string]string{"path": "a", "start_line": "2", "end_line": "2", "new": n, "expected_hash": digest([]byte("a\nb\n"))})
			errs <- e
		}(n)
	}
	wg.Wait()
	wins := 0
	for range 2 {
		if <-errs == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("winners %d", wins)
	}
}
func TestLineEditInterfaceReadCursor(t *testing.T) {
	for _, raw := range []string{"a\r\n\tβ  \r\nlast", "a\n\tb  \nlast\n", ""} {
		dir := t.TempDir()
		w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
		os.WriteFile(filepath.Join(dir, "a"), []byte(raw), 0600)
		for _, off := range []string{"1", "2", "3", "99"} {
			r, e := invoke(t, w, "read", map[string]string{"path": "a", "offset": off, "limit": "1"})
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Text) > w.Caps.MaxBytes {
				t.Fatal("cap")
			}
			if off == "1" && raw != "" {
				if !strings.Contains(r.Text, "shown_range=1-1") || !strings.Contains(r.Text, "next_offset=2") {
					t.Fatalf("cursor %q", r.Text)
				}
			} else if off == "3" || off == "99" || raw == "" {
				if strings.Contains(r.Text, "next_offset=") || ((off == "99" || raw == "") && !strings.Contains(r.Text, "shown_range=none")) {
					t.Fatalf("phantom %q", r.Text)
				}
			}
		}
	}
}

func TestLineEditInterfaceReadCapAndCancel(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	w.Caps.MaxBytes = 180
	raw := strings.Repeat("x", 150)
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte(raw), 0600)
	r, e := invoke(t, w, "read", map[string]string{"path": "a"})
	if e != nil || len(r.Text) > 180 || !r.Truncated || !strings.Contains(r.Text, "shown_range=none") || strings.Contains(r.Text, "next_offset=") {
		t.Fatalf("capped %+v %v", r, e)
	}
	for _, hash := range []string{"", "wrong"} {
		r, e = lineInvoke(defs, true, map[string]string{"path": "a", "start_line": "1", "end_line": "1", "new": "x", "expected_hash": hash})
		if e == nil || r.Changed {
			t.Fatal("unread hash published")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, d := range w.Definitions() {
		if d.Name == "edit" {
			r, e = d.Execute(ctx, tool.Args{"path": "a", "start_line": int64(1), "end_line": int64(1), "new": "x", "expected_hash": digest([]byte(raw))})
			if e == nil || r.Changed {
				t.Fatal("cancel published")
			}
		}
	}
	b, _ := os.ReadFile(p)
	if string(b) != raw {
		t.Fatal("guard changed bytes")
	}
}
