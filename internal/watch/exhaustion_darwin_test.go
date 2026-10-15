//go:build darwin

package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// kqueue opens descriptors for watched entries. Restrict only the owned test
// subprocess, never the parent test runner or the user's shell.
func TestNewReleasesDescriptorsOnExhaustion(t *testing.T) {
	mode := os.Getenv("CALIPER_WATCH_EXHAUSTION_CHILD")
	if mode == "" {
		for _, mode := range []string{"root", "nested", "close"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewReleasesDescriptorsOnExhaustion$", "-test.v")
				cmd.Env = append(os.Environ(), "CALIPER_WATCH_EXHAUSTION_CHILD="+mode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("owned child: %v\n%s", err, output)
				}
			})
		}
		return
	}
	root := t.TempDir()
	count := 256
	if mode == "close" {
		count = 4
	}
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("dir-%03d", i))
		if mode == "nested" {
			path = filepath.Join(root, "parent", fmt.Sprintf("dir-%03d", i))
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Max < 96 {
		t.Fatalf("test needs hard descriptor limit >=96: %d", limit.Max)
	}
	limit.Cur = 96
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if mode == "close" {
		var last *Watcher
		for i := 0; i < 20; i++ {
			w, err := New(root, DefaultConfig())
			if err != nil {
				t.Fatalf("watch %d: %v", i, err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			last = w
		}
		if err := last.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		watcher, err := New(root, DefaultConfig())
		if watcher != nil {
			defer watcher.Close()
		}
		if watcher != nil || (!errors.Is(err, syscall.EMFILE) && !errors.Is(err, syscall.ENFILE)) {
			t.Fatalf("expected nil watcher and actual exhaustion; watcher=%v error=%v", watcher != nil, err)
		}
	}
	// Successful cleanup must recover meaningful descriptor capacity, not just
	// return an error after leaking the partially installed recursive watches.
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for i := 0; i < 64; i++ {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("descriptor %d unavailable after failed watcher cleanup: %v", i, err)
		}
		files = append(files, f)
	}
}
