package tui

import (
	"os"
	"path/filepath"
	"specific-harness/internal/serve"
	"testing"
)

func fakeOllama(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "ollama"), []byte("#!/bin/sh\necho unexpected >> "+log+"\n"), 0700)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return log
}
func TestQuitReleasesOnlyOwnedProcesses(t *testing.T) {
	log := fakeOllama(t)
	owned, err := serve.Start(serve.Recipe{Name: "owned-neutral-fixture", Argv: []string{"sleep", "20"}}, filepath.Join(t.TempDir(), "owned.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Stop()
	sentinel, err := serve.Start(serve.Recipe{Name: "external-neutral-fixture", Argv: []string{"sleep", "20"}}, filepath.Join(t.TempDir(), "external.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Stop()
	m := New(testCfg(), "test")
	m.Serve.Spawned = map[string]*serve.Proc{"owned": owned}
	if err := m.CloseOwnedServing(); err != nil {
		t.Fatal(err)
	}
	if owned.PID() != 0 || sentinel.PID() == 0 {
		t.Fatal("ownership cleanup wrong")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("quit invoked unbound CLI")
	}
}
