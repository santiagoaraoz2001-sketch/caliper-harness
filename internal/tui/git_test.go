package tui

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitInfo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	init := exec.Command("git", "init", "-q")
	init.Dir = dir
	if err := init.Run(); err != nil {
		t.Fatal(err)
	}
	branch, base, err := gitInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch == "" {
		t.Error("fresh repo must report its unborn branch")
	}
	if base != filepath.Base(dir) {
		t.Errorf("base=%q", base)
	}
	if _, _, err := gitInfo(t.TempDir()); err == nil {
		t.Error("plain dir must fail")
	}
}
