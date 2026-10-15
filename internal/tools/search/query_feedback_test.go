package search

import (
	"os"
	"path/filepath"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestQueryFeedbackInterface(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	for _, p := range []string{"a.go", "a,b.go", "a.py"} {
		os.WriteFile(filepath.Join(dir, p), []byte("NEEDLE\n"), 0600)
	}
	for _, glob := range []string{"*.go,*.py", "*.missing"} {
		r := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE", "path": dir, "glob": glob})
		if r.Failed || r.Truncated || !strings.Contains(r.Text, "for this query only") || !strings.Contains(r.Text, "one pattern") {
			t.Fatalf("feedback %+v", r)
		}
	}
	for _, glob := range []string{"*.go", "a,b.go", "{*.go,*.py}"} {
		r := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE", "path": dir, "glob": glob})
		if !strings.Contains(r.Text, "NEEDLE") || strings.Contains(r.Text, "no matches") {
			t.Fatalf("valid glob %s %+v", glob, r)
		}
	}
	for _, cap := range []int{1, 10, 162, 1000} {
		c := testCfg()
		c.MaxBytes = cap
		for _, glob := range []string{"", "*.missing"} {
			r := grep(t, c, map[string]string{"pattern": map[bool]string{true: "ABSENT", false: "NEEDLE"}[glob == ""], "path": dir, "glob": glob})
			if len(r.Text) > cap || r.Failed || strings.Contains(r.Text, "next_offset=") || strings.Contains(r.Text, "more=") {
				t.Fatalf("tiny feedback %+v", r)
			}
			if r.Truncated != (cap < 162) {
				t.Fatalf("clipping %+v", r)
			}
		}
	}
	d := NewGrep(testCfg())
	a, q := tool.Validate(d, map[string]string{"pattern": "[", "path": dir})
	if q != "" {
		t.Fatal(q)
	}
	if _, e := d.Execute(t.Context(), a); e == nil {
		t.Fatal("regex error hidden")
	}
}
