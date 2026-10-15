package bash

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

// Actual shell/pipe execution: the output cap must bound capture, not kill work.
func TestCaptureBoundedDuringCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg()
	d := NewAt(cfg, dir)
	args, repair := tool.Validate(d, map[string]string{"cmd": "printf 'STDOUT-prefix\\n'; printf 'STDERR-prefix\\n' >&2; head -c 16777216 /dev/zero; head -c 16777216 /dev/zero >&2; printf settled > complete"})
	if repair != "" {
		t.Fatal(repair)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res, e := d.Execute(context.Background(), args)
	runtime.ReadMemStats(&after)
	spent := after.TotalAlloc - before.TotalAlloc
	marker, readErr := os.ReadFile(filepath.Join(dir, "complete"))
	if e != nil || res.ExitCode == nil || *res.ExitCode != 0 || res.Failed || readErr != nil || string(marker) != "settled" {
		t.Fatalf("actual command not settled: result=%+v error=%v marker=%q read=%v", res, e, marker, readErr)
	}
	if !res.Truncated || !strings.HasPrefix(res.Text, "STDOUT-prefix\nSTDERR-prefix\n") || !strings.Contains(res.Text, "[output truncated:") {
		t.Fatalf("capture disclosure/prefix lost: %q", res.Text)
	}
	if len(res.Text) > cfg.MaxBytes+150 {
		t.Fatalf("retained result exceeds cap plus marker: %d", len(res.Text))
	}
	t.Logf("actual32MiB combined output, GoTotalAlloc=%d bytes, retainedResult=%d, marker=%q exit=%d", spent, len(res.Text), marker, *res.ExitCode)
	if spent >= 8<<20 {
		t.Fatalf("capture allocated %d bytes for32MiB output despite MaxBytes=%d (want <8MiB)", spent, cfg.MaxBytes)
	}
}
func TestCaptureCombinedExitAndExactEdges(t *testing.T) {
	cfg := testCfg()
	cfg.MaxBytes = 8
	res, e := call(t, cfg, map[string]string{"cmd": "printf abc; printf defgh >&2; exit 7"})
	if e != nil || res.ExitCode == nil || *res.ExitCode != 7 || !res.Failed || res.Truncated || res.Text != "abcdefgh\n[exit code 7]" {
		t.Fatalf("exact-cap/nonzero combined: %+v %v", res, e)
	}
	res, e = call(t, cfg, map[string]string{"cmd": "printf abc; printf defghi >&2"})
	if e != nil || res.ExitCode == nil || *res.ExitCode != 0 || !res.Truncated || !strings.HasPrefix(res.Text, "abcdefgh\n[output truncated:") {
		t.Fatalf("one byte overflow: %+v %v", res, e)
	}
	cfg.MaxBytes = 1024
	cfg.MaxLines = 1
	res, e = call(t, cfg, map[string]string{"cmd": "printf 'first\\nsecond\\n'"})
	if e != nil || !res.Truncated || !strings.HasPrefix(res.Text, "first\n\n[output truncated:") || strings.Contains(res.Text, "second") {
		t.Fatalf("line contract: %+v %v", res, e)
	}
}
