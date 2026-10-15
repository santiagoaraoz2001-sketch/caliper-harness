package search

import (
	"context"
	"encoding/json"
	"path/filepath"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestSimplificationSearchLocatorsAndLimit(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	for _, d := range []tool.Definition{NewGrepAt(testCfg(), dir), NewFindFileAt(testCfg(), dir)} {
		pattern := "NEEDLE"
		if d.Name == "find_file" {
			pattern = "visible.txt"
		}
		for _, path := range []string{dir, "./", filepath.Join(dir, "visible.txt")} {
			raw, _ := json.Marshal(map[string]any{"pattern": pattern, "path": path})
			a, q := tool.ValidateJSON(d, string(raw))
			if q != "" {
				t.Fatal(q)
			}
			res, e := d.Execute(context.Background(), a)
			if e != nil || !strings.Contains(res.Text, "visible.txt") || strings.Contains(res.Text, dir) {
				t.Fatalf("noncanonical %s %+v %v", d.Name, res, e)
			}
		}
	}
}

func TestSimplificationInvalidGrepLimit(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	d := NewGrepAt(testCfg(), dir)
	for _, limit := range []int64{0, -1, 101} {
		a, q := tool.ValidateJSON(d, `{"pattern":"NEEDLE"}`)
		if q != "" {
			t.Fatal(q)
		}
		a["limit"] = limit
		if _, e := d.Execute(context.Background(), a); e == nil {
			t.Fatalf("silently expanded limit %d", limit)
		}
	}
}
