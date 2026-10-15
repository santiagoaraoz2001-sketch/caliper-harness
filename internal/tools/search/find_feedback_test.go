package search

import (
	"os"
	"path/filepath"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestFindQueryScopedFeedback(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "checks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checks/check_beta.py"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	def := NewFindAt(testCfg(), dir)
	for _, pattern := range []string{"check_beta.py,review_beta.py", "**/check_beta.py"} {
		args, q := tool.Validate(def, map[string]string{"pattern": pattern})
		if q != "" {
			t.Fatal(q)
		}
		res, e := def.Execute(t.Context(), args)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(pattern, ",") {
			if !strings.Contains(res.Text, "for this query only") || !strings.Contains(res.Text, "one pattern") || res.Failed {
				t.Fatalf("absence overclaimed: %+v", res)
			}
		} else if !strings.Contains(res.Text, "checks/check_beta.py") {
			t.Fatalf("existing path missing: %+v", res)
		}
	}
}
