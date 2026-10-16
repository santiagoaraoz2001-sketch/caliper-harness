package serve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"specific-harness/internal/bundle"
	"strings"
	"testing"
	"time"
)

func TestManagedBuiltinsAndCustomInterpreterRemainDistinct(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runtime"), 0755)
	os.WriteFile(filepath.Join(root, bundle.Marker), []byte(`{"format":1,"platform":"darwin-arm64"}`), 0644)
	recipes := DefaultRecipes(root, "local-model", "typed-decisions")
	if runtime.GOOS == "darwin" && (recipes[1].Argv[0] != bundle.Python(root, "worker") || !recipes[1].managedPython || !reflect.DeepEqual(recipes[1].Argv[1:3], []string{"-m", "mlx_lm.server"})) {
		t.Fatal(recipes[1])
	}
	sidecar, ok := SidecarRecipe(root, SidecarURL, "typed-decisions")
	if !ok || sidecar.Argv[0] != bundle.Python(root, "decision") || !sidecar.managedPython {
		t.Fatal(sidecar)
	}
	custom := Recipe{Name: "mlx", Argv: []string{"/user/python", "-m", "mlx_lm.server", "--model", "chosen"}}
	merged := MergeRecipes(recipes, []Recipe{custom})
	for _, r := range merged {
		if r.Name == "mlx" && (!reflect.DeepEqual(r.Argv, custom.Argv) || r.managedPython) {
			t.Fatal("custom interpreter was replaced", r)
		}
	}
}
func TestManagedProcessEnvironmentIsolatedButCustomUnchanged(t *testing.T) {
	t.Setenv("PYTHONHOME", "poison-home")
	t.Setenv("PYTHONPATH", "poison-path")
	t.Setenv("HF_HOME", "preserved-models")
	for _, managed := range []bool{true, false} {
		root := t.TempDir()
		exe := filepath.Join(root, "python3")
		out := filepath.Join(root, "environment")
		if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s|%s|%s|%s' \"$PYTHONHOME\" \"$PYTHONPATH\" \"$PYTHONNOUSERSITE\" \"$HF_HOME\" > \"$1\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
		proc, err := StartContext(context.Background(), Recipe{Name: "owned-env-fixture", Argv: scriptCmd(t, exe, out), managedPython: managed}, filepath.Join(root, "log"))
		if err != nil {
			t.Fatal(err)
		}
		<-proc.done
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if managed && string(data) != "||1|preserved-models" {
			t.Fatal(string(data))
		}
		if !managed && !strings.Contains(string(data), "poison-home|poison-path|") {
			t.Fatal("custom environment changed", string(data))
		}
	}
}

// Opt-in actual Python entrypoint proof. No weights or inference are used.
func TestManagedNativeBootstrapHelp(t *testing.T) {
	root := os.Getenv("CALIPER_MANAGED_SMOKE_DIR")
	if root == "" {
		t.Skip("opt-in private Mac bundle")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PYTHONHOME", "/does-not-exist")
	t.Setenv("PYTHONPATH", "/does-not-exist")
	for _, r := range DefaultRecipes(root, "", "typed-decisions") {
		if r.Name != "mlx" && r.Name != "sidecar" {
			continue
		}
		dir := t.TempDir()
		if r.Name == "mlx" {
			r.Argv = []string{r.Argv[0], filepath.Join(root, "python", "worker", "server.py"), "--model", dir, "--help"}
		} else {
			r.Argv = append(r.Argv, "--help")
		}
		r.Dir = dir
		log := filepath.Join(dir, "bootstrap.log")
		p, err := StartContext(context.Background(), r, log)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
		case <-time.After(20 * time.Second):
			_ = p.Stop()
			t.Fatal("bootstrap did not settle")
		}
		data, err := os.ReadFile(log)
		if err != nil || !p.cmd.ProcessState.Success() || !strings.Contains(string(data), "usage:") {
			t.Fatalf("%s: %v\n%s", r.Name, err, data)
		}
		t.Logf("%s: private native bootstrap --help exited0, no system Python onPATH, poisoned ambient imports, no checkpoint loading", r.Name)
	}
}
