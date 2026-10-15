package search

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func testCfg() config.SearchConfig {
	return config.SearchConfig{MaxCaptureBytes: 16777216, MaxMatches: 100, MaxBytes: 65536, MaxLineLength: 1000}
}

func requireRG(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed — skipping live rg test")
	}
}

// fixture builds a git repo so .gitignore is honored: visible.txt and
// ignored.txt both contain NEEDLE, but only visible.txt may match.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.txt", "first NEEDLE line\nsecond line\n")
	write("ignored.txt", "NEEDLE in ignored file\n")
	write(".gitignore", "ignored.txt\n")
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git init: %v", err)
		}
	}
	return dir
}

func grep(t *testing.T, cfg config.SearchConfig, slots map[string]string) tool.Result {
	t.Helper()
	args, repair := tool.Validate(NewGrep(cfg), slots)
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	res, err := NewGrep(cfg).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	return res
}

func TestGrepFindsMatchWithLocation(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	res := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE", "path": dir})
	if !strings.Contains(res.Text, "visible.txt") || !strings.Contains(res.Text, "NEEDLE") {
		t.Errorf("text=%q", res.Text)
	}
	if strings.Contains(res.Text, "ignored.txt") {
		t.Errorf("gitignored file leaked into results: %q", res.Text)
	}
}

func TestRegisteredZeroOutcomeMetadata(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	for _, d := range []tool.Definition{NewGrepAt(testCfg(), dir), NewFindFileAt(testCfg(), dir)} {
		reg := tool.NewRegistry()
		reg.Register(d)
		d, _ = reg.Lookup(d.Name)
		for _, pattern := range []string{"absent_pattern", "NEEDLE"} {
			if d.Name == "find_file" && pattern == "NEEDLE" {
				pattern = "visible.txt"
			}
			raw, _ := json.Marshal(map[string]string{"pattern": pattern})
			args, repair := tool.ValidateJSON(d, string(raw))
			if repair != "" {
				t.Fatal(repair)
			}
			result, err := d.Execute(context.Background(), args)
			encoded, _ := json.Marshal(result)
			var flags map[string]any
			_ = json.Unmarshal(encoded, &flags)
			zero := pattern == "absent_pattern"
			if err != nil || result.Failed || result.Blocked || (flags["NoMatches"] == true) != zero {
				t.Fatalf("actual %s query %q metadata/effect: %s %v", d.Name, pattern, encoded, err)
			}
			if zero && !strings.Contains(result.Text, "this query only") {
				t.Fatal("zero falsely implies absence")
			}
		}
	}
}

func TestGrepLiteralBeatsRegex(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	res := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE|zzzz", "path": dir, "literal": "true"})
	if !strings.Contains(res.Text, "no matches") {
		t.Errorf("literal pipe must not regex-match: %q", res.Text)
	}
}

func TestGrepGlobFilters(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	res := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE", "path": dir, "glob": "*.md"})
	if !strings.Contains(res.Text, "no matches") {
		t.Errorf("glob excluding .txt must match nothing: %q", res.Text)
	}
}

func TestGrepGlobDoesNotLeakIgnored(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	// rg whitelists explicit globs past .gitignore; filtering must be ours.
	res := grep(t, testCfg(), map[string]string{"pattern": "NEEDLE", "path": dir, "glob": "*.txt"})
	if !strings.Contains(res.Text, "visible.txt") {
		t.Errorf("text=%q", res.Text)
	}
	if strings.Contains(res.Text, "ignored.txt") {
		t.Errorf("glob leaked gitignored file: %q", res.Text)
	}
}

func TestGrepNoMatchesIsNotError(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	res := grep(t, testCfg(), map[string]string{"pattern": "ABSENT_XYZ", "path": dir})
	if !strings.Contains(res.Text, "for this query only") || res.Truncated {
		t.Errorf("text=%q truncated=%v", res.Text, res.Truncated)
	}
}

func TestGrepLimitTruncates(t *testing.T) {
	requireRG(t)
	cfg := testCfg()
	cfg.MaxMatches = 2
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("NEEDLE line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	res := grep(t, cfg, map[string]string{"pattern": "NEEDLE", "path": dir})
	if !res.Truncated || !strings.Contains(res.Text, "[results truncated:") {
		t.Errorf("truncated=%v text=%q", res.Truncated, res.Text)
	}
}

func TestGrepMissingRGNamesFix(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty of binaries
	d := NewGrep(testCfg())
	args, repair := tool.Validate(d, map[string]string{"pattern": "x", "path": "."})
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	_, err := d.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "brew install ripgrep") {
		t.Errorf("error=%v", err)
	}
}

func TestFindListsGlob(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	d := NewFind(testCfg())
	args, repair := tool.Validate(d, map[string]string{"pattern": "*.txt", "path": dir})
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	res, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("find failed: %v", err)
	}
	if !strings.Contains(res.Text, "visible.txt") {
		t.Errorf("text=%q", res.Text)
	}
	if strings.Contains(res.Text, "ignored.txt") {
		t.Errorf("gitignored file leaked: %q", res.Text)
	}
}
