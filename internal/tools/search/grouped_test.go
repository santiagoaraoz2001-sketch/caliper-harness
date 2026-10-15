package search

import (
	"context"
	"encoding/json"
	"fmt"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"testing"
	"unicode/utf8"
)

type grepGroup struct {
	Path  string `json:"path"`
	Lines []struct {
		Line      int    `json:"line"`
		Source    string `json:"source"`
		Match     bool   `json:"match"`
		LF        bool   `json:"lf"`
		Truncated bool   `json:"truncated"`
	} `json:"lines"`
}

func grepGroups(t *testing.T, text string) []grepGroup {
	t.Helper()
	var groups []grepGroup
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&groups); err != nil {
		t.Fatalf("source references not grouped JSON: %v %q", err, text)
	}
	return groups
}

// Decode the new public representation so historical path/context assertions stay exact.
func grepLocators(t *testing.T, text string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	var groups []grepGroup
	if err := dec.Decode(&groups); err != nil {
		t.Fatal(err, text)
	}
	var rows []string
	for _, group := range groups {
		previous := 0
		for _, row := range group.Lines {
			if previous != 0 && row.Line > previous+1 {
				rows = append(rows, "--")
			}
			sep := "-"
			if row.Match {
				sep = ":"
			}
			rows = append(rows, fmt.Sprintf("%s%s%d%s%s", group.Path, sep, row.Line, sep, row.Source))
			previous = row.Line
		}
	}
	return strings.Join(rows, "\n") + text[dec.InputOffset():]
}
func groupedCall(t *testing.T, dir string, cfg config.SearchConfig, slots map[string]any) (tool.Result, error) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(NewGrepAt(cfg, dir))
	d, _ := reg.Lookup("grep")
	raw, err := json.Marshal(slots)
	if err != nil {
		t.Fatal(err)
	}
	args, q := tool.ValidateJSON(d, string(raw))
	if q != "" {
		t.Fatal(q)
	}
	return d.Execute(context.Background(), args)
}
func TestGroupedGrepExactSource(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	name := "quote: \"é🌱.go"
	rows := []string{"before", "\tHIT \"é🌱\" <tag>\r", "after", "gap", "gap", "HIT second", "tail"}
	r19Write(t, dir, name, strings.Join(rows, "\n"))
	res, err := groupedCall(t, dir, testCfg(), map[string]any{"pattern": "HIT", "context": int64(1)})
	if err != nil || res.Truncated || res.NoMatches {
		t.Fatalf("actual grep %+v %v", res, err)
	}
	groups := grepGroups(t, res.Text)
	if len(groups) != 1 || groups[0].Path != "./"+name {
		t.Fatalf("path grouping %+v", groups)
	}
	want := []int{1, 2, 3, 5, 6, 7}
	if len(groups[0].Lines) != len(want) {
		t.Fatalf("gaps/context %+v", groups)
	}
	for i, row := range groups[0].Lines {
		if row.Line != want[i] || row.Source != rows[row.Line-1] || row.Match != (row.Line == 2 || row.Line == 6) || row.LF != (row.Line < len(rows)) || row.Truncated {
			t.Fatalf("actual source row %+v", row)
		}
	}
	cfg := testCfg()
	cfg.MaxBytes = len(res.Text)
	exact, err := groupedCall(t, dir, cfg, map[string]any{"pattern": "HIT", "context": int64(1)})
	if err != nil || exact.Text != res.Text || exact.Truncated {
		t.Fatalf("exact encoded boundary %+v %v", exact, err)
	}
	cfg.MaxBytes--
	smaller, err := groupedCall(t, dir, cfg, map[string]any{"pattern": "HIT", "context": int64(1)})
	if err == nil && (len(smaller.Text) > cfg.MaxBytes || !smaller.Truncated) {
		t.Fatalf("encoded overflow %+v", smaller)
	}
}
func TestGroupedGrepByteRecovery(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	r19Write(t, dir, "a.go", strings.Repeat("before\nHIT "+strings.Repeat("é", 30)+"\nafter\n", 4))
	cfg := testCfg()
	cfg.MaxBytes = 400
	offset := 0
	var got []int
	for page := 0; page < 5; page++ {
		res, err := groupedCall(t, dir, cfg, map[string]any{"pattern": "HIT", "context": int64(1), "offset": int64(offset)})
		if err != nil || len(res.Text) > cfg.MaxBytes || !utf8.ValidString(res.Text) {
			t.Fatalf("bounded page %+v %v", res, err)
		}
		next, more := pagingCursor(t, res.Text)
		count := 0
		for _, group := range grepGroups(t, res.Text) {
			for _, row := range group.Lines {
				if row.Match {
					got = append(got, row.Line)
					count++
				}
			}
		}
		if count == 0 || next-offset != count {
			t.Fatalf("cursor skipped actual match %d→%d %+v", offset, next, res)
		}
		offset = next
		if !more {
			break
		}
	}
	if fmt.Sprint(got) != "[2 5 8 11]" {
		t.Fatalf("actual recovered matches %v", got)
	}
	no, err := groupedCall(t, dir, cfg, map[string]any{"pattern": "HIT", "offset": int64(40)})
	if err != nil || !no.NoMatches || no.Failed || no.Blocked {
		t.Fatalf("zero unchanged %+v %v", no, err)
	}
	cfg.MaxLineLength = 60
	cfg.MaxBytes = 512
	clipped, err := groupedCall(t, dir, cfg, map[string]any{"pattern": "HIT"})
	if err != nil || !clipped.Truncated || !strings.Contains(clipped.Text, "[line cut]") {
		t.Fatalf("clip %+v %v", clipped, err)
	}
	for _, g := range grepGroups(t, clipped.Text) {
		for _, row := range g.Lines {
			if !utf8.ValidString(row.Source) || !row.Truncated {
				t.Fatal("split UTF8")
			}
		}
	}
}

func TestGroupedGrepNoContextGapRecovery(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	r19Write(t, dir, "a.go", "HIT first\nunmatched gap\nHIT second\n")
	offset := 0
	for _, want := range []int{1, 3} {
		res, err := groupedCall(t, dir, testCfg(), map[string]any{"pattern": "HIT", "limit": int64(1), "context": int64(0), "offset": int64(offset)})
		if err != nil {
			t.Fatal(err)
		}
		groups := grepGroups(t, res.Text)
		if len(groups) != 1 || groups[0].Path != "./a.go" || len(groups[0].Lines) != 1 {
			t.Fatal(groups)
		}
		row := groups[0].Lines[0]
		if row.Line != want || !row.Match || !row.LF || row.Truncated || row.Source != map[int]string{1: "HIT first", 3: "HIT second"}[want] {
			t.Fatal(row)
		}
		next, more := pagingCursor(t, res.Text)
		if next != offset+1 || more != (want == 1) {
			t.Fatal(next, more)
		}
		offset = next
	}
}
