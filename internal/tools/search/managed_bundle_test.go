package search

import (
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/bundle"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

// Execute the real search owner with its test executable placed inside the
// disposable managed bundle. No product resolver override or model is used.
func TestManagedBundleSearchWithoutSystemRG(t *testing.T) {
	expected := os.Getenv("CALIPER_MANAGED_SMOKE_DIR")
	if expected == "" || bundle.Root() != expected {
		t.Skip("opt-in executable-relative managed bundle probe")
	}
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("CALIPER_BUNDLED_SEARCH\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []tool.Definition{NewGrepAt(config.Defaults().Tools.Search, dir), NewFindAt(config.Defaults().Tools.Search, dir)} {
		pattern := "CALIPER_BUNDLED_SEARCH"
		if d.Name == "find" {
			pattern = "*.txt"
		}
		args, repair := tool.Validate(d, map[string]string{"pattern": pattern})
		if repair != "" {
			t.Fatal(repair)
		}
		result, err := d.Execute(context.Background(), args)
		if err != nil || !strings.Contains(result.Text, "sample.txt") {
			t.Fatal(d.Name, err, result.Text)
		}
		t.Logf("%s: actual bundled ripgrep executes with empty PATH", d.Name)
	}
}
