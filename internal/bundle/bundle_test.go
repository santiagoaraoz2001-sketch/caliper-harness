package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	script := []byte("#!/bin/sh\nexit 0\n")
	files := []struct {
		path string
		data []byte
	}{
		{filepath.Join(root, Marker), []byte(`{"format":1,"platform":"` + platform() + `"}`)},
		{Python(root, "worker"), script},
		{Python(root, "decision"), script},
		{filepath.Join(root, "bin", rgFileName()), script},
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path, f.data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestManagedRootAndMissingAssetsDoNotFallBack(t *testing.T) {
	root := fixture(t)
	t.Setenv("PATH", t.TempDir())
	if RootAt(filepath.Join(root, "caliper-harness")) != root {
		t.Fatal("not executable-relative")
	}
	if err := Check(root); err != nil {
		t.Fatal(err)
	}
	rg, err := ToolAt(root, "rg")
	if err != nil || rg != filepath.Join(root, "bin", rgFileName()) {
		t.Fatal(rg, err)
	}
	if err := os.Remove(filepath.Join(root, "bin", rgFileName())); err != nil {
		t.Fatal(err)
	}
	if _, err = ToolAt(root, "rg"); err == nil || !strings.Contains(err.Error(), "complete Caliper folder") {
		t.Fatal("missing bundled rg fell back")
	}
}
func TestRequiredBuildCannotLoseRuntimeFolder(t *testing.T) {
	old := Required
	Required = "1"
	defer func() { Required = old }()
	root := t.TempDir()
	if RootAt(filepath.Join(root, "caliper-harness")) != root {
		t.Fatal("missing marker silently un-managed the executable")
	}
	if err := Check(root); err == nil || !strings.Contains(err.Error(), "manifest missing") {
		t.Fatal("missing runtime accepted")
	}
}
func TestManagedPythonEnvKeepsModelLocationsAndScrubsPython(t *testing.T) {
	env := PythonEnv([]string{"PATH=/usr/bin", "PYTHONHOME=poison", "HF_HOME=/existing/models", "VIRTUAL_ENV=/other", "PYTHONPLATLIBDIR=poison"}, []string{"PYTHONPATH=poison", "PYTHONNOUSERSITE=0", "HF_HUB_OFFLINE=1"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "poison") || strings.Contains(joined, "VIRTUAL_ENV") || strings.Contains(joined, "PYTHONNOUSERSITE=0") {
		t.Fatal(joined)
	}
	for _, want := range []string{"HF_HOME=/existing/models", "HF_HUB_OFFLINE=1", "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1"} {
		if !strings.Contains(joined, want) {
			t.Fatal(joined)
		}
	}
}
