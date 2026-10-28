package bash

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/tool"
)

func TestOwnedServiceChild(t *testing.T) {
	path := os.Getenv("CALIPER_SERVICE_TEST_READY")
	if path == "" {
		t.Skip("owned child fixture")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("http://"+l.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "owned service fixture") }))
}

func TestServiceSurvivesCallAndStopsOnlyItsOwnedTree(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "owned ready.txt")
	unrelatedReady := filepath.Join(dir, "unrelated ready.txt")
	unrelated := exec.Command(exe, "-test.run=^TestOwnedServiceChild$")
	unrelated.Env = append(os.Environ(), "CALIPER_SERVICE_TEST_READY="+unrelatedReady)
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	readURL := func(path string) string {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			if raw, e := os.ReadFile(path); e == nil {
				return string(raw)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("service did not publish its address")
		return ""
	}
	health := func(url string) bool {
		r, e := client.Get(url)
		if e != nil {
			return false
		}
		defer r.Body.Close()
		return r.StatusCode == 200
	}
	unrelatedURL := readURL(unrelatedReady)
	owner := &ServiceOwner{}
	defer owner.Close()
	d := NewAtOwned(testCfg(), dir, owner)
	if !strings.Contains(d.CallTemplate(), `"cmd"`) {
		t.Fatal("conditional cmd lost the ordinary current-call example")
	}
	if _, err := d.Execute(t.Context(), tool.Args{"service": "status", "timeout": int64(5)}); err == nil {
		t.Fatal("service silently ignored timeout")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\\''") + "'" }
	command := "CALIPER_SERVICE_TEST_READY=" + quote(ready) + " " + quote(exe) + " -test.run=^TestOwnedServiceChild$"
	r, err := d.Execute(context.Background(), tool.Args{"service": "start", "cmd": command})
	if err != nil || r.Failed || !strings.Contains(r.Text, "unverified") {
		t.Fatalf("start %+v %v", r, err)
	}
	url := readURL(ready)
	if !health(url) {
		t.Fatal("service died after launch call returned")
	}
	if _, err := d.Execute(t.Context(), tool.Args{"cmd": "echo next turn"}); err != nil {
		t.Fatal(err)
	}
	if !health(url) {
		t.Fatal("ordinary call ended session service")
	}
	if _, err := d.Execute(t.Context(), tool.Args{"service": "start", "cmd": command}); err == nil {
		t.Fatal("duplicate service replaced without explicit stop")
	}
	if _, err := d.Execute(t.Context(), tool.Args{"service": "stop"}); err != nil {
		t.Fatal(err)
	}
	if health(url) || !health(unrelatedURL) {
		t.Fatal("stop did not isolate owned service")
	}
	_ = os.Remove(ready)
	if _, err := d.Execute(t.Context(), tool.Args{"service": "start", "cmd": command}); err != nil {
		t.Fatal(err)
	}
	url = readURL(ready)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if health(url) || !health(unrelatedURL) {
		t.Fatal("session cleanup did not isolate owned service")
	}
	if _, err := d.Execute(t.Context(), tool.Args{"service": "start", "cmd": command}); err == nil {
		t.Fatal("closed owner accepted service")
	}
}

func TestPostStartPipeFailurePreservesOutputAndNamesLifecycle(t *testing.T) {
	r, err := New(testCfg()).Execute(t.Context(), tool.Args{"cmd": "printf retained; sleep 4 &"})
	if err == nil || !r.Failed || !strings.Contains(r.Text, "retained") || !strings.Contains(err.Error(), "did not settle cleanly") || strings.Contains(err.Error(), "is bash installed") {
		t.Fatalf("lifecycle classified incorrectly: %+v %v", r, err)
	}
}

func TestFailedServiceStatusProvidesBoundedOwnedDiagnostics(t *testing.T) {
	owner := &ServiceOwner{}
	defer owner.Close()
	d := NewAtOwned(testCfg(), t.TempDir(), owner)
	start, err := d.Execute(t.Context(), tool.Args{"service": "start", "cmd": "printf '%05000d' 0 >&2; printf '\\nPREVIEW_FAILED: missing fixture asset\\n' >&2; exit 7"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(start.Text, "PREVIEW_FAILED") || strings.Contains(start.Text, "log excerpt") {
		t.Fatal("start receipt flooded diagnostics")
	}
	deadline := time.Now().Add(5 * time.Second)
	for owner.proc.PID() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if owner.proc.PID() != 0 {
		t.Fatal("failing service did not settle")
	}
	status, err := d.Execute(t.Context(), tool.Args{"service": "status"})
	if err != nil || !strings.Contains(status.Text, "No running") || !strings.Contains(status.Text, "PREVIEW_FAILED: missing fixture asset") {
		t.Fatalf("failed preview diagnostics unavailable: %+v %v", status, err)
	}
	marker := "may be truncated):\n"
	_, excerpt, ok := strings.Cut(status.Text, marker)
	if !ok || len(excerpt) > 2048 || len(excerpt) != 2048 {
		t.Fatalf("diagnostic bound lost: %d %q", len(excerpt), status.Text)
	}
	// Replacing a remembered path cannot turn status into an arbitrary file read.
	old := owner.log + ".original"
	if err := os.Rename(owner.log, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(owner.log, []byte("UNRELATED_FILE_CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err = d.Execute(t.Context(), tool.Args{"service": "status"})
	if err != nil || strings.Contains(status.Text, "UNRELATED_FILE_CONTENT") || !strings.Contains(status.Text, "identity changed") {
		t.Fatalf("status read replacement file: %+v %v", status, err)
	}
}
