package search

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/tools/process"
)

// Reflection only bridges the new config field onto the unchanged old source:
// absence must still fail the real execution/memory assertions, not compilation.
func captureCfg(n int) config.SearchConfig {
	c := config.Defaults().Tools.Search
	if f := reflect.ValueOf(&c).Elem().FieldByName("MaxCaptureBytes"); f.IsValid() {
		f.SetInt(int64(n))
	}
	return c
}
func settledRG(t *testing.T) (string, string) {
	t.Helper()
	real, err := exec.LookPath("rg")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "settled")
	if runtime.GOOS == "windows" {
		// CreateProcess cannot run a bare script; a .cmd shim keeps the
		// same contract: forward args, settle, then write the marker.
		// `set /p` writes exactly "settled" with no trailing newline.
		script := "@echo off\r\n\"" + real + "\" %*\r\nset st=%errorlevel%\r\n<nul set /p \"_=settled\">\"" + marker + "\"\r\nexit /b %st%\r\n"
		if err := os.WriteFile(filepath.Join(bin, "rg.cmd"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		script := "#!/bin/sh\n" + quote(real) + " \"$@\"\nstatus=$?\nprintf settled > " + quote(marker) + "\nexit \"$status\"\n"
		if err := os.WriteFile(filepath.Join(bin, "rg"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return real, marker
}
func TestSearchCaptureActualFlood(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flood.txt"), []byte("HIT "+strings.Repeat("x", 32<<20)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	real, marker := settledRG(t)
	cfg := captureCfg(64 << 10)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res, err := pagingCall(t, context.Background(), dir, "grep", cfg, map[string]string{"pattern": "HIT"})
	runtime.ReadMemStats(&after)
	spent := after.TotalAlloc - before.TotalAlloc
	b, e := os.ReadFile(marker)
	if e != nil || string(b) != "settled" {
		t.Fatalf("actual rg not settled: marker=%q err=%v", b, e)
	}
	t.Logf("actual rg=%s 32MiB source line; GoTotalAlloc=%d returned=%d error=%v", real, spent, len(res.Text), err)
	if err == nil || !res.Failed || !res.Truncated || !strings.Contains(err.Error(), "max_capture_bytes") {
		t.Errorf("raw overflow must fail/truncate, result=%+v error=%v", res, err)
	}
	if strings.Contains(res.Text, "next_offset=") || res.Text == "no matches" {
		t.Errorf("overflow claims usable result: %q", res.Text)
	}
	if spent >= 8<<20 {
		t.Errorf("raw capture allocated %d despite64KiB test budget; want<8MiB", spent)
	}
}

func TestSearchCaptureFindAndActualErrors(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		if err := os.WriteFile(filepath.Join(dir, strings.Repeat("a", 40)+string(rune('A'+i))), []byte("HIT\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := pagingCall(t, t.Context(), dir, "find", captureCfg(128), map[string]string{"pattern": "*"})
	if err == nil || !res.Failed || !res.Truncated || strings.Contains(res.Text, "next_offset=") || res.Text == "no matches" {
		t.Fatalf("find overflow result=%+v err=%v", res, err)
	}
	cfg := captureCfg(16 << 20)
	res, err = pagingCall(t, t.Context(), dir, "grep", cfg, map[string]string{"pattern": "MISSING"})
	if err != nil || !strings.Contains(res.Text, "for this query only") || res.Failed {
		t.Fatalf("actual exit1: %+v %v", res, err)
	}
	_, err = pagingCall(t, t.Context(), dir, "grep", cfg, map[string]string{"pattern": "["})
	if err == nil || !strings.Contains(err.Error(), "regex parse error") {
		t.Fatalf("actual stderr/nonzero: %v", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("actual exit cause lost: %v", err)
	}
	_, err = pagingCall(t, t.Context(), dir, "find", cfg, map[string]string{"pattern": "*", "path": "missing"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("actual find stderr: %v", err)
	}
}

func TestSearchCaptureSharedBoundaryAndCancel(t *testing.T) {
	c := searchCapture{remaining: 8}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(stream int) {
			defer wg.Done()
			n, e := (captureWriter{&c, stream}).Write([]byte("abcd"))
			if n != 4 || e != nil {
				t.Errorf("write %d %v", n, e)
			}
		}(i)
	}
	wg.Wait()
	if c.overflow || c.buf[0].Len()+c.buf[1].Len() != 8 {
		t.Fatalf("exact shared cap retained=%d overflow=%t", c.buf[0].Len()+c.buf[1].Len(), c.overflow)
	}
	n, e := (captureWriter{&c, 0}).Write([]byte("x"))
	if n != 1 || e != nil || !c.overflow || c.buf[0].Len()+c.buf[1].Len() != 8 {
		t.Fatal("one-byte overflow not drained/bounded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "started")
	cmd := process.Command(ctx, "sh", "-c", "printf partial; printf MORE >&2; printf start > \"$1\"; sleep 10", "sh", marker)
	done := make(chan struct{})
	var out, stderr []byte
	var over bool
	var err error
	go func() { out, stderr, over, err = captureSearch(ctx, cmd, 8); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e := os.Stat(marker); e == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("owned command never admitted")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("owned cancellation not settled")
	}
	if !errors.Is(err, context.Canceled) || !over || len(out)+len(stderr) > 8 || !strings.Contains(err.Error(), "max_capture_bytes") {
		t.Fatalf("simultaneous overflow/cancel out=%q stderr=%q overflow=%t err=%v", out, stderr, over, err)
	}
}
