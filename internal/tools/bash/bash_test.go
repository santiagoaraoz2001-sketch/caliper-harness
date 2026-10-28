package bash

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func testCfg() config.BashConfig {
	return config.BashConfig{DefaultTimeout: 5, MaxTimeout: 10, MaxBytes: 1024, MaxLines: 50}
}

func call(t *testing.T, cfg config.BashConfig, slots map[string]string) (tool.Result, error) {
	t.Helper()
	d := New(cfg)
	args, repair := tool.Validate(d, slots)
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	return d.Execute(context.Background(), args)
}

func TestEcho(t *testing.T) {
	res, err := call(t, testCfg(), map[string]string{"cmd": "echo hello"})
	if err != nil {
		shell, shellErr := ShellPath()
		t.Fatalf("echo failed: %v; shell=%q lookup=%v; partial=%q truncated=%v", err, shell, shellErr, res.Text, res.Truncated)
	}
	if !strings.Contains(res.Text, "hello") || res.Truncated {
		t.Errorf("text=%q truncated=%v", res.Text, res.Truncated)
	}
}

func TestTimeoutNamesSeconds(t *testing.T) {
	_, err := call(t, testCfg(), map[string]string{"cmd": "sleep 5", "timeout": "1"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out after 1s") {
		t.Errorf("error=%q", err)
	}
}

func TestOverMaxTimeoutRefused(t *testing.T) {
	_, err := call(t, testCfg(), map[string]string{"cmd": "echo x", "timeout": "99"})
	if err == nil || !strings.Contains(err.Error(), "exceeds the 10s limit") {
		t.Errorf("error=%v", err)
	}
}

func TestFloodTruncatesWithMarker(t *testing.T) {
	res, err := call(t, testCfg(), map[string]string{"cmd": "seq 1 5000"})
	if err != nil {
		t.Fatalf("seq failed: %v", err)
	}
	if !res.Truncated || !strings.Contains(res.Text, "[output truncated:") {
		t.Errorf("truncated=%v, missing marker", res.Truncated)
	}
}

func TestExitCodeRidesAlong(t *testing.T) {
	res, err := call(t, testCfg(), map[string]string{"cmd": "echo out; exit 3"})
	if err != nil {
		t.Fatalf("non-zero exit must not error: %v", err)
	}
	if !strings.Contains(res.Text, "out") || !strings.Contains(res.Text, "[exit code 3]") {
		t.Errorf("text=%q", res.Text)
	}
}

func TestNonZeroExitMarksFailed(t *testing.T) {
	res, err := call(t, testCfg(), map[string]string{"cmd": "exit 3"})
	if err != nil {
		t.Fatalf("non-zero exit must not error: %v", err)
	}
	if !res.Failed {
		t.Errorf("Failed=false for exit 3 — the loop cannot see the failure")
	}
	ok, err := call(t, testCfg(), map[string]string{"cmd": "echo hello"})
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if ok.Failed {
		t.Errorf("Failed=true for a clean exit")
	}
}

func TestDefaultTimeoutApplies(t *testing.T) {
	d := New(testCfg())
	args, _ := tool.Validate(d, map[string]string{"cmd": "true"})
	if args["timeout"] != int64(5) {
		t.Errorf("timeout=%v, want 5", args["timeout"])
	}
}
