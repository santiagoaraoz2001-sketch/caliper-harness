package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func TestSimplificationAbsoluteMutationAndGuards(t *testing.T) {
	w := &Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	p := filepath.Join(w.Path, "a")
	os.WriteFile(p, []byte("one\ntwo\n"), 0600)
	if _, e := navigationCall(t, defs, "read", map[string]any{"path": p, "limit": 1}); e != nil {
		t.Fatal(e)
	}
	a := map[string]any{"path": p, "start_line": 2, "end_line": 2, "new": "bad\n", "expected_hash": digest([]byte("one\ntwo\n"))}
	if _, e := navigationCall(t, defs, "edit", a); e == nil {
		t.Fatal("absolute path bypassed missing coverage")
	}
	a["start_line"], a["end_line"], a["new"] = 1, 1, "ONE\n"
	result, e := navigationCall(t, defs, "edit", a)
	if e != nil || !result.Changed || result.FileEffect.Path != "a" {
		t.Fatalf("absolute grounded edit: %+v %v", result, e)
	}
	if _, e = navigationCall(t, defs, "edit", a); e == nil {
		t.Fatal("stale hash admitted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("safe"), 0600)
	os.Symlink(outside, filepath.Join(w.Path, "link"))
	for _, path := range []string{outside, filepath.Join(w.Path, "link"), "../escape"} {
		if _, e := navigationCall(t, defs, "write", map[string]any{"path": path, "content": "bad", "expected_hash": digest([]byte("safe"))}); e == nil {
			t.Fatalf("unsafe write %s", path)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "safe" {
		t.Fatal("outside changed")
	}
	for _, path := range []string{filepath.Join(w.Path, "created"), "./relative"} {
		if _, e := navigationCall(t, defs, "write", map[string]any{"path": path, "content": "created"}); e != nil {
			t.Fatal(e)
		}
	}
}

func TestSimplificationSourceNotOutputCap(t *testing.T) {
	w := &Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	raw := "package p\n" + strings.Repeat("// filler\n", 10000) + "func Target() {}\n"
	os.WriteFile(filepath.Join(w.Path, "large.go"), []byte(raw), 0600)
	for _, args := range []map[string]any{{"path": "large.go", "definition": "Target"}, {"path": "large.go", "outline": true}} {
		res, e := navigationCall(t, defs, "read", args)
		if e != nil || !strings.Contains(res.Text, "Target") || len(res.Text) > w.Caps.MaxBytes {
			t.Fatalf("large Go navigation: %+v %v", res, e)
		}
	}
	next := strings.Replace(raw, "Target()", "Changed()", 1)
	res, e := navigationCall(t, defs, "edit", map[string]any{"path": "large.go", "old": "Target()", "new": "Changed()", "expected_hash": digest([]byte(raw))})
	b, _ := os.ReadFile(filepath.Join(w.Path, "large.go"))
	if e != nil || !res.Changed || string(b) != next || len(res.Text) > w.Caps.MaxBytes {
		t.Fatalf("large exact edit: %+v %v", res, e)
	}
	for _, size := range []int{w.Caps.MaxBytes - 1, w.Caps.MaxBytes, w.Caps.MaxBytes + 1} {
		content := strings.Repeat("x", size)
		res, e := navigationCall(t, defs, "write", map[string]any{"path": "growth", "content": content})
		if e != nil {
			t.Fatal(e)
		}
		grown := content + "y"
		res, e = navigationCall(t, defs, "write", map[string]any{"path": "growth", "content": grown, "expected_hash": digest([]byte(content))})
		if e != nil || !res.Changed || len(res.Text) > w.Caps.MaxBytes {
			t.Fatalf("growth: %+v %v", res, e)
		}
		os.Remove(filepath.Join(w.Path, "growth"))
	}
	for _, bad := range []string{"x\x00", "x\xff"} {
		for _, d := range defs {
			if d.Name == "write" {
				if _, e := d.Execute(context.Background(), tool.Args{"path": "bad", "content": bad}); e == nil {
					t.Fatal("invalid text accepted")
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, d := range defs {
		if d.Name == "edit" {
			res, e := d.Execute(ctx, tool.Args{"path": "large.go", "old": "Changed()", "new": "no()", "expected_hash": digest([]byte(next))})
			if e == nil || res.Changed {
				t.Fatal("cancelled edit")
			}
		}
	}
}

func TestSimplificationZeroSourcePage(t *testing.T) {
	w := &Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	w.Caps.MaxBytes = 512
	os.WriteFile(filepath.Join(w.Path, "long.go"), []byte("package p\nfunc X(){ /*"+strings.Repeat("x", 1000)+"*/ }\n"), 0600)
	for _, args := range []map[string]any{{"path": "long.go", "offset": 2}, {"path": "long.go", "definition": "X"}} {
		res, e := navigationCall(t, w.Definitions(), "read", args)
		if e != nil || !res.NoMatches || !res.Truncated || !strings.Contains(res.Text, "shown_range=none") {
			t.Fatalf("zero source not marked: %+v %v", res, e)
		}
	}
}

// Recorded inputs remain external evidence; only the staged path is rebased.
func TestSimplificationRecordedNativeEdit(t *testing.T) {
	proofPath, sourcePath := os.Getenv("HARNESS_EDIT_PROOF"), os.Getenv("HARNESS_EDIT_SOURCE")
	if proofPath == "" || sourcePath == "" {
		t.Skip("set HARNESS_EDIT_PROOF and HARNESS_EDIT_SOURCE for exact native replay")
	}
	var proof struct {
		Arguments map[string]any `json:"arguments"`
	}
	raw, e := os.ReadFile(proofPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &proof); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	if digest(source) != proof.Arguments["expected_hash"] {
		t.Fatal("original fixture hash differs")
	}
	old, newText := proof.Arguments["old"].(string), proof.Arguments["new"].(string)
	if strings.Count(string(source), old) != 1 {
		t.Fatal("recorded old source does not match exactly")
	}
	w := &Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	p := filepath.Join(w.Path, "app.go")
	os.WriteFile(p, source, 0600)
	proof.Arguments["path"] = p
	res, e := navigationCall(t, w.Definitions(), "edit", proof.Arguments)
	got, _ := os.ReadFile(p)
	want := strings.Replace(string(source), old, newText, 1)
	if e != nil || !res.Changed || string(got) != want || res.FileEffect.AfterHash != digest(got) {
		t.Fatalf("recorded exact native edit: %+v %v", res, e)
	}
}
