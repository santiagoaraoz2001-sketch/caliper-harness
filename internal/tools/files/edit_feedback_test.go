package files

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

// The unchanged native790/792 requests follow an actual two-line read. Refusals
// must identify the failed condition without publishing; both edit routes remain usable.
func TestCapturedEditFeedbackAndCorrectedCheck(t *testing.T) {
	const original = "def fahrenheit_to_celsius(value):\n    return value - 32 * 5 / 9\n"
	const hash = "d568badb3c1c963b56bc81989c17e3e09ebc43438426bc8c463d535352c1b76b"
	dir := t.TempDir()
	path := filepath.Join(dir, "converter.py")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	reg := tool.NewRegistry()
	for _, def := range w.Definitions() {
		reg.Register(def)
	}
	invoke := func(name, raw string) (tool.Result, error) {
		t.Helper()
		def, _ := reg.Lookup(name)
		args, repair := tool.ValidateJSON(def, raw)
		if repair != "" {
			t.Fatal(repair)
		}
		return def.Execute(context.Background(), args)
	}
	read, err := invoke("read", `{"path":"./converter.py","limit":500}`)
	if err != nil || !strings.Contains(read.Text, hash) || !strings.Contains(read.Text, "shown_range=1-2; EOF=true; truncated=false") || !strings.Contains(read.Text, original) {
		t.Fatalf("read %q %v", read.Text, err)
	}
	if !strings.Contains(read.Text, "metadata not source lines") {
		t.Errorf("ambiguous source numbering %q", read.Text)
	}
	cases := []struct{ name, raw, want string }{
		{"captured790", `{"new":"def fahrenheit_to_celsius(value):\n    return (value - 32) * 5 / 9","path":"./converter.py","start_line":2,"end_line":3,"expected_hash":"` + hash + `"}`, "range 2-3 outside source lines 1-2"},
		{"captured792", `{"end_line":3,"expected_hash":"` + hash + `","new":"def fahrenheit_to_celsius(value):\n    return (value - 32) * 5 / 9","old":"def fahrenheit_to_celsius(value):\n    return value - 32 * 5 / 9","path":"./converter.py","start_line":2}`, "omit old for a line range"},
		{"missing_endpoint", `{"path":"./converter.py","start_line":2,"new":"x","expected_hash":"` + hash + `"}`, "needs both integer start_line and end_line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := invoke("edit", c.raw)
			if err == nil || !strings.Contains(err.Error(), c.want) || res.Changed || res.FileEffect != nil {
				t.Errorf("specific refusal %+v %v", res, err)
			}
			if c.name == "captured790" && err != nil && !strings.Contains(err.Error(), "metadata is not source") {
				t.Errorf("metadata clarification %v", err)
			}
			b, e := os.ReadFile(path)
			if e != nil || string(b) != original {
				t.Fatalf("refused effect %q %v", b, e)
			}
		})
	}
	res, err := invoke("edit", `{"path":"./converter.py","old":"value - 32 * 5 / 9","new":"(value - 32) * 5 / 9","expected_hash":"`+hash+`"}`)
	if err != nil || !res.Changed || res.FileEffect == nil {
		t.Fatalf("corrected exact span %+v %v", res, err)
	}
	checks := "import unittest\nfrom converter import fahrenheit_to_celsius\nclass ConversionTests(unittest.TestCase):\n    def test_freezing(self): self.assertAlmostEqual(fahrenheit_to_celsius(32), 0)\n    def test_boiling(self): self.assertAlmostEqual(fahrenheit_to_celsius(212), 100)\n"
	if err = os.WriteFile(filepath.Join(dir, "test_converter.py"), []byte(checks), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "python3", "-m", "unittest", "-v")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Ran 2 tests") || !strings.Contains(string(output), "OK") {
		t.Fatalf("actual checker %v %s", err, output)
	}
	t.Logf("actual corrected checker: %s", output)
	// The valid observed range remains legal, without old and without inserted endings.
	read, err = invoke("read", `{"path":"./converter.py"}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": "./converter.py", "start_line": 1, "end_line": 2, "new": string(b), "expected_hash": digest(b)})
	res, err = invoke("edit", string(raw))
	if err != nil || res.Changed || res.FileEffect != nil {
		t.Fatalf("valid observed noop range %+v %v", res, err)
	}
}
