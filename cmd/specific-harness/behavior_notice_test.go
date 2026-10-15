package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/config"
	"strings"
	"testing"
)

func TestBehaviorLaunchCLINoticeBeforeRefusal(t *testing.T) {
	dir := t.TempDir()
	c := config.Defaults()
	c.Store.Path = filepath.Join(dir, "db")
	c.Behavior.Path = filepath.Join(dir, "behavior")
	c.Model.Name = "fixture"
	c.Behavior.MaxTokens = 2
	c.Behavior.CharsPerToken = 2
	// No HTTP can be reached: unknown approach refuses before readiness/model.
	c.Model.Server = "http://127.0.0.1:1/v1"
	c.Decision.Server = "http://127.0.0.1:1"
	if err := os.WriteFile(c.Behavior.Path, []byte("αβγδεζ"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	if err := config.Save(cfg, c); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$")
	cmd.Env = append(os.Environ(), "HARNESS_CLI_PROCESS=1", "HARNESS_CLI_ARGV="+strings.Join([]string{"launch", "missing", "--config", cfg}, "\x1f"))
	raw, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || !strings.Contains(string(raw), "note: behavior file (6 chars) exceeds ~2 tokens — keeping the last 4 chars") || !strings.Contains(string(raw), "missing") {
		t.Fatalf("notice/refusal %v %s", err, raw)
	}
}
