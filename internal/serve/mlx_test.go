package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestD09PrepareNativeLocalOffline(t *testing.T) {
	p := t.TempDir()
	makeAsset(t, p, false)
	script := filepath.Join(t.TempDir(), "bootstrap.py")
	os.WriteFile(script, []byte("# component-only placeholder never executed\n"), 0600)
	original := Recipe{Runtime: "mlx", Argv: []string{"explicit-python", "-m", "mlx_lm.server", "--model", "old-network-name", "--port", "18440"}, URL: "http://127.0.0.1:18440/v1"}
	r, err := PrepareMLX(original, script, p)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(p)
	if r.Argv[0] != "explicit-python" || r.Argv[1] != script || r.Argv[3] != canonical || !strings.Contains(strings.Join(r.Env, " "), "HF_HUB_OFFLINE=1") {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(original.Argv, []string{"explicit-python", "-m", "mlx_lm.server", "--model", "old-network-name", "--port", "18440"}) {
		t.Fatal("input recipe mutated")
	}
	if _, err := PrepareMLX(original, script, filepath.Join(p, "absent-repo-name")); err == nil {
		t.Fatal("remote/unresolved model admitted")
	}
	os.Remove(filepath.Join(p, "model.safetensors"))
	if _, err := PrepareMLX(original, script, p); err == nil {
		t.Fatal("incomplete admitted")
	}
}
func TestD09PrepareNativeShebangAndUnknownWrapper(t *testing.T) {
	p := t.TempDir()
	makeAsset(t, p, false)
	script := filepath.Join(t.TempDir(), "bootstrap.py")
	os.WriteFile(script, []byte("# no execution"), 0600)
	dir := t.TempDir()
	bin := filepath.Join(dir, "mlx_lm.server")
	os.WriteFile(bin, []byte("#!/explicit/interpreter\n"), 0700)
	r, err := PrepareMLX(Recipe{Argv: []string{bin, "--model", p}}, script, p)
	if err != nil || r.Argv[0] != "/explicit/interpreter" {
		t.Fatalf("%+v %v", r, err)
	}
	os.WriteFile(bin, []byte("#!/usr/bin/env python3\n"), 0700)
	if _, err := PrepareMLX(Recipe{Argv: []string{bin, "--model", p}}, script, p); err == nil {
		t.Fatal("guessed env interpreter")
	}
	if _, err := PrepareMLX(Recipe{Argv: []string{"sh", "-c", "arbitrary wrapper"}}, script, p); err == nil {
		t.Fatal("unknown wrapper emulated")
	}
}
func TestD09OwnedReadinessAndSettledStop(t *testing.T) {
	// This process is a neutral owned sleep, not MLX. HTTP native facts are doubles.
	p, err := Start(Recipe{Name: "owned-neutral-fixture", Argv: []string{"sleep", "20"}}, filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	model := t.TempDir()
	p.localPath = model
	pid := p.PID()
	var bad atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if bad.Load() {
				w.Write([]byte(`{"pid":1,"path":"wrong","loaded":true,"tokenizer":true,"generation":true}`))
				return
			}
			w.Write([]byte(`{"pid":` + strconv.Itoa(pid) + `,"path":` + strconv.Quote(model) + `,"loaded":true,"tokenizer":true,"generation":true}`))
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p.url = srv.URL + "/v1"
	if h, err := OwnedMLXReady(context.Background(), srv.Client(), p); err != nil || !h.Up || h.Runtime != "mlx" || h.APIBase != p.url {
		t.Fatalf("%+v %v", h, err)
	}
	bad.Store(true)
	if _, err := OwnedMLXReady(context.Background(), srv.Client(), p); err == nil {
		t.Fatal("mismatched native proof accepted")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if p.PID() != 0 {
		t.Fatal("Stop returned before owned reaping")
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("owned process not settled")
	}
	if _, err := OwnedMLXReady(context.Background(), srv.Client(), p); err == nil {
		t.Fatal("dead owned worker ready")
	}
}

func TestD09OwnedReadinessCancelsAPIBaseReadback(t *testing.T) {
	p, err := Start(Recipe{Name: "owned-neutral-fixture", Argv: []string{"sleep", "20"}}, filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	p.localPath = t.TempDir()
	pid := p.PID()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"pid":` + strconv.Itoa(pid) + `,"path":` + strconv.Quote(p.localPath) + `,"loaded":true,"tokenizer":true,"generation":true}`))
			return
		}
		close(entered)
		<-release
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	p.url = srv.URL + "/v1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := OwnedMLXReady(ctx, srv.Client(), p); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("readback ignored cancel %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("API base readback outlived cancellation")
	}
	close(release)
}

func TestD09OwnedReadinessRejectsDeathDuringAPIReadback(t *testing.T) {
	p, err := Start(Recipe{Name: "owned-neutral-fixture", Argv: []string{"sleep", "20"}}, filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	p.localPath = t.TempDir()
	pid := p.PID()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"pid":` + strconv.Itoa(pid) + `,"path":` + strconv.Quote(p.localPath) + `,"loaded":true,"tokenizer":true,"generation":true}`))
			return
		}
		close(entered)
		<-release
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	p.url = srv.URL + "/v1"
	done := make(chan error, 1)
	go func() { _, err := OwnedMLXReady(context.Background(), srv.Client(), p); done <- err }()
	<-entered
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("dead/reaped owned process declared ready after API readback")
	}
}
