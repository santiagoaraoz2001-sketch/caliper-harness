package files

import (
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadGroundingBlindRangeRefusesBeforePublication(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	defs := w.Definitions()
	raw := "one\ntwo\nthree\n"
	p := filepath.Join(dir, "a")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "./a", "limit": "1"}); err != nil {
		t.Fatal(err)
	}
	res, err := lineInvoke(defs, true, map[string]string{"path": "a", "start_line": "3", "end_line": "3", "new": "bad\n", "expected_hash": digest([]byte(raw))})
	got, _ := os.ReadFile(p)
	if err == nil || res.Changed || string(got) != raw || !strings.Contains(err.Error(), "read") {
		t.Fatalf("blind range admitted: res=%+v err=%v bytes=%q", res, err, got)
	}
}

func TestPublicationReceiptShowsActualScopeAndSource(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	raw := "before\n\told  \r\nend"
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte(raw), 0600)
	res, err := invoke(t, w, "edit", map[string]string{"path": "a", "old": "\told  \r\n", "new": "\tnew  \n", "expected_hash": digest([]byte(raw))})
	if err != nil || !res.Changed || !strings.Contains(res.Text, "exact-span") || !strings.Contains(res.Text, "- \told  \r\n") || !strings.Contains(res.Text, "+ \tnew  \n") || !strings.Contains(res.Text, "replacement_final_LF=true") {
		t.Fatalf("opaque publication: %+v %v", res, err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "before\n\tnew  \nend" {
		t.Fatalf("publication bytes %q", got)
	}
}

func invokeDefinitions(t *testing.T, defs []tool.Definition, name string, slots map[string]string) (tool.Result, error) {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			args, q := tool.Validate(d, slots)
			if q != "" {
				t.Fatal(q)
			}
			return d.Execute(context.Background(), args)
		}
	}
	t.Fatal(name)
	return tool.Result{}, nil
}

func TestReadGroundingPagesVersionsAndRegistryOwnership(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	a, b := w.Definitions(), w.Definitions()
	raw := "a\nb\nc\nd\n"
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte(raw), 0600)
	edit := func(defs []tool.Definition, start, end string, new string) (tool.Result, error) {
		got, _ := os.ReadFile(p)
		return lineInvoke(defs, true, map[string]string{"path": "./a", "start_line": start, "end_line": end, "new": new, "expected_hash": digest(got)})
	}
	read := func(defs []tool.Definition, off, limit string) {
		t.Helper()
		if _, e := invokeDefinitions(t, defs, "read", map[string]string{"path": "sub/../a", "offset": off, "limit": limit}); e != nil {
			t.Fatal(e)
		}
	}
	read(a, "3", "1")
	read(a, "1", "1")
	if r, e := edit(a, "1", "3", "BAD\n"); e == nil || r.Changed {
		t.Fatal("gap admitted")
	}
	read(a, "2", "1")
	if r, e := edit(b, "1", "3", "BAD\n"); e == nil || r.Changed {
		t.Fatal("sibling read granted")
	}
	if r, e := edit(a, "1", "3", "joined\n"); e != nil || !r.Changed || !strings.Contains(r.Text, "inclusive lines 1-3") {
		t.Fatalf("actual contiguous pages rejected %+v %v", r, e)
	}
	if r, e := edit(a, "1", "1", "BAD\n"); e == nil || r.Changed {
		t.Fatal("publication retained old authority")
	}
	read(a, "1", "1")
	now, _ := os.ReadFile(p)
	// Real sibling publications restore identical bytes; the earlier grant is stale even at the same hash.
	if _, e := invokeDefinitions(t, b, "write", map[string]string{"path": "a", "content": "temporary\n", "expected_hash": digest(now)}); e != nil {
		t.Fatal(e)
	}
	if _, e := invokeDefinitions(t, b, "write", map[string]string{"path": "a", "content": string(now), "expected_hash": digest([]byte("temporary\n"))}); e != nil {
		t.Fatal(e)
	}
	if r, e := edit(a, "1", "1", "BAD\n"); e == nil || r.Changed {
		t.Fatal("sibling publication/revert retained grant")
	}
	read(a, "1", "1")
	fresh := (&Workspace{Path: dir, Caps: w.Caps}).Definitions()
	if r, e := edit(fresh, "1", "1", "BAD\n"); e == nil || r.Changed {
		t.Fatal("fresh workspace inherited authority")
	}
	os.WriteFile(p, []byte("external\n"), 0600)
	if r, e := edit(a, "1", "1", "BAD\n"); e == nil || r.Changed {
		t.Fatal("changed hash retained grant")
	}
}

func TestReadGroundingZeroShownAndReadErrorsDoNotGrant(t *testing.T) {
	dir := t.TempDir()
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	w.Caps.MaxBytes = 180
	defs := w.Definitions()
	raw := strings.Repeat("é", 70)
	p := filepath.Join(dir, "a")
	os.WriteFile(p, []byte(raw), 0600)
	r, e := invokeDefinitions(t, defs, "read", map[string]string{"path": "a"})
	if e != nil || !r.Truncated || !strings.Contains(r.Text, "shown_range=none") {
		t.Fatalf("zero shown setup %+v %v", r, e)
	}
	invalidLimit := strconv.Itoa(w.Caps.MaxLines + 1)
	for _, tc := range []struct{ offset, limit, field, value string }{
		{"0", "1", "offset", "0"}, {"500", invalidLimit, "limit", invalidLimit},
	} {
		r, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": tc.offset, "limit": tc.limit})
		if err == nil || !strings.Contains(err.Error(), tc.field+"="+tc.value) || r.Changed || r.Text != "" {
			t.Errorf("invalid %s=%s must be identified without a source grant: result=%+v error=%v", tc.field, tc.value, r, err)
		} else if tc.field == "limit" && (!strings.Contains(err.Error(), "1.."+strconv.Itoa(w.Caps.MaxLines)) || strings.Contains(err.Error(), "offset")) {
			t.Errorf("valid offset must not be blamed for invalid limit: %v", err)
		}
	}
	if _, err := invokeDefinitions(t, defs, "read", map[string]string{"path": "a", "offset": "99"}); err != nil {
		t.Fatal("a valid offset beyond EOF is not a validation failure", err)
	}
	w.Caps.MaxBytes = 1000 // Isolate authority failure from the receipt-byte admission guard.
	r, e = lineInvoke(defs, true, map[string]string{"path": "a", "start_line": "1", "end_line": "1", "new": "x", "expected_hash": digest([]byte(raw))})
	if e == nil || r.Changed {
		t.Fatal("unreturned line granted")
	}
}

func TestPublicationReceiptAggregateAndByteBounds(t *testing.T) {
	for _, scenario := range []string{"balanced", "borrowed", "byte clipped", "metadata cannot fit", "empty create"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			raw := strings.Repeat("old\n", 20)
			next := strings.Repeat("新\t  \r\n", 20)
			if scenario == "borrowed" {
				raw = "old\n"
			}
			if scenario == "byte clipped" {
				raw = strings.Repeat("é", 180)
				next = strings.Repeat("末", 120)
				w.Caps.MaxBytes = 400
			}
			if scenario == "metadata cannot fit" {
				raw = "old\n"
				next = "new\n"
				w.Caps.MaxBytes = 200
			}
			if scenario == "empty create" {
				raw = ""
				next = ""
			}
			p := filepath.Join(dir, "a")
			args := map[string]string{"path": "a", "content": next}
			if scenario != "empty create" {
				os.WriteFile(p, []byte(raw), 0600)
				args["expected_hash"] = digest([]byte(raw))
			}
			r, e := invoke(t, w, "write", args)
			got, readErr := os.ReadFile(p)
			if scenario == "metadata cannot fit" {
				if e == nil || r.Changed || string(got) != raw {
					t.Fatalf("unreportable effect %+v %v %q", r, e, got)
				}
				return
			}
			if e != nil || !r.Changed || readErr != nil || string(got) != next || len(r.Text) > w.Caps.MaxBytes || !utf8.ValidString(r.Text) || r.FileEffect == nil || r.FileEffect.AfterHash != digest(got) {
				t.Fatalf("publication/cap %+v %v %q", r, e, got)
			}
			count, minus, plus := 0, 0, 0
			for _, line := range strings.Split(r.Text, "\n") {
				if strings.HasPrefix(line, "- ") {
					minus++
					count++
				}
				if strings.HasPrefix(line, "+ ") {
					plus++
					count++
				}
			}
			if count > 16 {
				t.Fatalf("aggregate preview lines %d", count)
			}
			if scenario == "balanced" && (minus != 8 || plus != 8 || !r.Truncated) {
				t.Fatalf("unbalanced %d/%d %+v", minus, plus, r)
			}
			if scenario == "borrowed" && (minus != 1 || plus != 15 || !r.Truncated) {
				t.Fatalf("unused budget not borrowed %d/%d", minus, plus)
			}
			if scenario == "byte clipped" && (!r.Truncated || !strings.HasSuffix(r.Text, "[preview omitted]") || !strings.Contains(r.Text, "file_final_LF=false")) {
				t.Fatalf("clipping unlabelled %+v", r)
			}
			if scenario == "empty create" && (r.Truncated || !strings.Contains(r.Text, "whole-file creation") || !strings.Contains(r.Text, "0→0 file lines")) {
				t.Fatalf("empty fictional preview %+v", r)
			}
		})
	}
}

func TestReadGroundingDirectoryAliasesAndBoundaries(t *testing.T) {
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, "real"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("real", filepath.Join(dir, "alias")); e != nil {
		t.Fatal(e)
	}
	rootAlias := filepath.Join(t.TempDir(), "root")
	if e := os.Symlink(dir, rootAlias); e != nil {
		t.Fatal(e)
	}
	raw := "one\ntwo\n"
	file := filepath.Join(dir, "real", "a")
	if e := os.WriteFile(file, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	w := &Workspace{Path: rootAlias, Caps: config.Defaults().Tools.Bash}
	reader, writer := w.Definitions(), w.Definitions()
	read := func(path string) {
		t.Helper()
		r, e := invokeDefinitions(t, reader, "read", map[string]string{"path": path})
		if e != nil || !strings.Contains(r.Text, raw) {
			t.Fatal(r, e)
		}
	}
	edit := func(path string) (tool.Result, error) {
		return lineInvoke(reader, true, map[string]string{"path": path, "start_line": "1", "end_line": "2", "new": "NEW\n", "expected_hash": digest([]byte(raw))})
	}
	read("alias/a")
	for _, content := range []string{"temporary\n", raw} {
		b, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		r, e := invokeDefinitions(t, writer, "write", map[string]string{"path": "real/a", "content": content, "expected_hash": digest(b)})
		if e != nil || !r.Changed || r.FileEffect.Path != "real/a" {
			t.Fatal(r, e)
		}
	}
	if r, e := edit("alias/a"); e == nil || r.Changed {
		t.Fatalf("alias restore retained authority: %+v %v", r, e)
	}
	read("alias/a")
	if r, e := edit("real/a"); e != nil || !r.Changed || r.FileEffect.Path != "real/a" {
		t.Fatal(r, e)
	}
	if e := os.Symlink("a", filepath.Join(dir, "real", "file-link")); e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("SECRET\n"), 0600)
	if e := os.Symlink(outside, filepath.Join(dir, "escape")); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"alias/file-link", "escape/secret"} {
		if r, e := invokeDefinitions(t, reader, "read", map[string]string{"path": path}); e == nil || r.Text != "" {
			t.Fatalf("boundary read admitted: %+v %v", r, e)
		}
	}
	for _, path := range []string{"missing/a", "escape/new"} {
		if r, e := invokeDefinitions(t, writer, "write", map[string]string{"path": path, "content": "BAD\n"}); e == nil || r.Changed {
			t.Fatalf("boundary create admitted: %+v %v", r, e)
		}
	}
	if _, e := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(e) {
		t.Fatal("outside creation", e)
	}
	r, e := invokeDefinitions(t, writer, "write", map[string]string{"path": "alias/new", "content": ""})
	if e != nil || !r.Changed || !r.FileEffect.Created || r.FileEffect.Path != "real/new" {
		t.Fatal(r, e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "real", "new"))
	if e != nil || len(b) != 0 {
		t.Fatal(b, e)
	}
}

func TestReadGroundingCaseAliasesAndHardlinkPublication(t *testing.T) {
	for _, variant := range []string{"file-case", "directory-case", "hardlink"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			raw := "one\ntwo\n"
			original, alias := "a", "A"
			if variant == "directory-case" {
				if e := os.Mkdir(filepath.Join(dir, "Sub"), 0700); e != nil {
					t.Fatal(e)
				}
				original, alias = "Sub/a", "sub/a"
			}
			if variant == "hardlink" {
				alias = "b"
			}
			file := filepath.Join(dir, original)
			if e := os.WriteFile(file, []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if variant == "hardlink" {
				if e := os.Link(file, filepath.Join(dir, alias)); e != nil {
					t.Fatal(e)
				}
			}
			x, e := os.Stat(filepath.Join(dir, alias))
			if os.IsNotExist(e) {
				t.Skip("owned filesystem has distinct case names")
			}
			if e != nil {
				t.Fatal(e)
			}
			y, e := os.Stat(file)
			if e != nil || !os.SameFile(x, y) {
				t.Fatal("not an actual alias", e)
			}
			w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
			reader, writer := w.Definitions(), w.Definitions()
			if _, e := invokeDefinitions(t, reader, "read", map[string]string{"path": original}); e != nil {
				t.Fatal(e)
			}
			writePath := alias
			if variant == "hardlink" {
				writePath = original
				if _, e := invokeDefinitions(t, reader, "read", map[string]string{"path": alias}); e != nil {
					t.Fatal(e)
				}
			}
			for _, content := range []string{"temporary\n", raw} {
				b, e := os.ReadFile(filepath.Join(dir, writePath))
				if e != nil {
					t.Fatal(e)
				}
				r, e := invokeDefinitions(t, writer, "write", map[string]string{"path": writePath, "content": content, "expected_hash": digest(b)})
				if e != nil || !r.Changed {
					t.Fatal(r, e)
				}
			}
			target := original
			if variant == "hardlink" {
				target = alias
			}
			r, e := lineInvoke(reader, true, map[string]string{"path": target, "start_line": "1", "end_line": "2", "new": "NEW\n", "expected_hash": digest([]byte(raw))})
			if variant == "hardlink" {
				if e != nil || !r.Changed {
					t.Fatalf("separate unchanged hardlink grant lost: %+v %v", r, e)
				}
				b, e := os.ReadFile(file)
				if e != nil || string(b) != raw {
					t.Fatal("hardlink publication changed other entry", string(b), e)
				}
			} else {
				b, _ := os.ReadFile(file)
				if e == nil || r.Changed || string(b) != raw {
					t.Fatalf("case alias stale publication: %+v %v %q", r, e, b)
				}
				if _, e := invokeDefinitions(t, reader, "read", map[string]string{"path": alias}); e != nil {
					t.Fatal(e)
				}
				if r, e := lineInvoke(reader, true, map[string]string{"path": alias, "start_line": "1", "end_line": "2", "new": "FRESH\n", "expected_hash": digest([]byte(raw))}); e != nil || !r.Changed {
					t.Fatal(r, e)
				}
			}
		})
	}
}
