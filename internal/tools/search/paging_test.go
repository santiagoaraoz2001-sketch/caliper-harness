package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func pagingCall(t *testing.T, ctx context.Context, dir, name string, cfg config.SearchConfig, slots map[string]string) (tool.Result, error) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(NewFindAt(cfg, dir))
	reg.Register(NewGrepAt(cfg, dir))
	d, ok := reg.Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	a, q := tool.Validate(d, slots)
	if q != "" {
		return tool.Result{}, fmt.Errorf("validation: %s", q)
	}
	return d.Execute(ctx, a)
}
func pagingCursor(t *testing.T, text string) (int, bool) {
	t.Helper()
	m := regexp.MustCompile(`offset=\d+ next_offset=(\d+) more=(true|false)`).FindStringSubmatch(text)
	if len(m) != 3 {
		t.Fatalf("actual search lacks recovery cursor: %q", text)
	}
	n, e := strconv.Atoi(m[1])
	if e != nil {
		t.Fatal(e)
	}
	return n, m[2] == "true"
}
func TestPagingFindRealRecovery(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	cfg := testCfg()
	cfg.MaxMatches = 2
	for _, n := range []string{"e.go", "c.go", "a.go", "d.go", "b.go"} {
		r19Write(t, dir, n, "HIT\n")
	}
	var got []string
	off := 0
	for pages := 0; pages < 4; pages++ {
		slots := map[string]string{"pattern": "*.go"}
		if off > 0 {
			slots["offset"] = strconv.Itoa(off)
		}
		res, e := pagingCall(t, t.Context(), dir, "find", cfg, slots)
		if e != nil {
			t.Fatal(e)
		}
		next, more := pagingCursor(t, res.Text)
		for _, l := range strings.Split(res.Text, "\n") {
			if strings.HasSuffix(l, ".go") {
				got = append(got, l)
			}
		}
		if next <= off {
			t.Fatal("zero progress")
		}
		off = next
		if !more {
			break
		}
	}
	if strings.Join(got, ",") != "./a.go,./b.go,./c.go,./d.go,./e.go" || off != 5 {
		t.Fatalf("actual pages %v offset%d", got, off)
	}
}
func TestPagingGrepRealFilteredRecovery(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	cfg := testCfg()
	cfg.MaxMatches = 2
	r19Write(t, dir, "a.go", strings.Repeat("HIT\n", 6))
	r19Write(t, dir, "b.go", "HIT\nHIT\n")
	r19Write(t, dir, "0-excluded.txt", strings.Repeat("HIT\n", 9))
	var got []string
	off := 0
	for page := 0; page < 6; page++ {
		slots := map[string]string{"pattern": "HIT", "glob": "*.go", "limit": "2"}
		if off > 0 {
			slots["offset"] = strconv.Itoa(off)
		}
		res, e := pagingCall(t, t.Context(), dir, "grep", cfg, slots)
		if e != nil {
			t.Fatal(e)
		}
		next, more := pagingCursor(t, res.Text)
		for _, group := range grepGroups(t, res.Text) {
			for _, row := range group.Lines {
				if row.Match {
					got = append(got, fmt.Sprintf("%s:%d:%s", group.Path, row.Line, row.Source))
				}
			}
		}
		if next-off != 2 {
			t.Fatalf("global/page mismatch %d→%d %q", off, next, res.Text)
		}
		off = next
		if !more {
			break
		}
	}
	want := []string{}
	for i := 1; i <= 6; i++ {
		want = append(want, fmt.Sprintf("./a.go:%d:HIT", i))
	}
	want = append(want, "./b.go:1:HIT", "./b.go:2:HIT")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("filtered/samefile pages %v", got)
	}
}
func TestPagingByteCapCursorAndContext(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	r19Write(t, dir, "a.go", "BEFORE\nHIT1 "+strings.Repeat("x", 60)+"\nAFTER\nGAP\nHIT2 "+strings.Repeat("x", 60)+"\nAFTER2\nHIT3 "+strings.Repeat("x", 60)+"\nAFTER3\nHIT4 "+strings.Repeat("x", 60)+"\nAFTER4\n")
	cfg := testCfg()
	cfg.MaxMatches = 4
	cfg.MaxBytes = 400 // Encoded references plus the unchanged recovery footer must fit.
	off := 0
	got := []string{}
	for page := 0; page < 6; page++ {
		slots := map[string]string{"pattern": "HIT", "context": "1"}
		if off > 0 {
			slots["offset"] = strconv.Itoa(off)
		}
		res, e := pagingCall(t, t.Context(), dir, "grep", cfg, slots)
		if e != nil {
			t.Fatal(e)
		}
		if len(res.Text) > cfg.MaxBytes {
			t.Fatal("footer exceeds cap")
		}
		next, more := pagingCursor(t, res.Text)
		count := 0
		for _, group := range grepGroups(t, res.Text) {
			for _, row := range group.Lines {
				if row.Match {
					got = append(got, fmt.Sprintf("%s:%d:%s", group.Path, row.Line, strings.Fields(row.Source)[0]))
					count++
				}
			}
		}
		if next-off != count || count == 0 {
			t.Fatalf("cursor skipped/badcontext %q", res.Text)
		}
		off = next
		if !more {
			break
		}
	}
	if len(got) != 4 || off != 4 || strings.Join(got, ",") != "./a.go:2:HIT1,./a.go:5:HIT2,./a.go:7:HIT3,./a.go:9:HIT4" {
		t.Fatalf("byte-recovered %v", got)
	}
	res, e := pagingCall(t, t.Context(), dir, "grep", cfg, map[string]string{"pattern": "HIT", "offset": "40"})
	if e != nil || !strings.Contains(res.Text, "for this query only") {
		t.Fatalf("beyond end %+v %v", res, e)
	}
}
func TestPagingRefusalsBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	trap := filepath.Join(dir, "rg")
	if e := os.WriteFile(trap, []byte("#!/bin/sh\nprintf invoked > \"$MARKER\"\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS == "windows" {
		// LookPath finds rg only through PATHEXT; without a .cmd twin the
		// trap could never fire and this test could not catch a spawn.
		if e := os.WriteFile(filepath.Join(dir, "rg.cmd"), []byte("@echo invoked >\"%MARKER%\"\r\n"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	marker := filepath.Join(dir, "called")
	t.Setenv("PATH", dir)
	t.Setenv("MARKER", marker)
	for _, name := range []string{"find", "grep"} {
		for _, off := range []string{"-1", strconv.FormatInt(math.MaxInt64, 10)} {
			_, e := pagingCall(t, t.Context(), dir, name, testCfg(), map[string]string{"pattern": "HIT", "offset": off})
			if e == nil || !strings.Contains(e.Error(), "offset") {
				t.Fatalf("offset admitted %s %s %v", name, off, e)
			}
		}
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatalf("invalid offset spawned: %v", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e := pagingCall(t, ctx, dir, "grep", testCfg(), map[string]string{"pattern": "HIT"})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
