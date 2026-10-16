package serve

import (
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/decision"
	"testing"
)

func TestRecipeModelBindingAndInstallation(t *testing.T) {
	original := Recipe{Name: "custom", Argv: []string{"missing-server-test-executable", "--model", "{model}", "--alias={model}"}}
	bound, err := BindModel(original, "/models/space name;literal.gguf")
	if err != nil || !reflect.DeepEqual(bound.Argv[2:], []string{"/models/space name;literal.gguf", "--alias=/models/space name;literal.gguf"}) || original.Argv[2] != "{model}" {
		t.Fatalf("binding %v %v", bound, err)
	}
	if _, err := BindModel(original, ""); err == nil {
		t.Fatal("empty binding admitted")
	}
	if _, err := original.Executable(); err == nil {
		t.Fatal("missing executable called installed")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := (Recipe{Argv: []string{exe}}).Executable(); err != nil || got != exe {
		t.Fatalf("installation %q %v", got, err)
	}
	if _, err := (Recipe{}).Executable(); err == nil {
		t.Fatal("external endpoint called installed")
	}
	for _, runtime := range []string{"openai", "ollama", "unknown"} {
		if _, ok := SidecarRecipe("/repo", "http://localhost:8080/v1", "judge", decision.Selection{Runtime: runtime}); ok {
			t.Fatalf("unsupported judge runtime %q starts native sidecar", runtime)
		}
	}
}

func TestDiscoveryGGUFDoesNotClaimMLXCompatibility(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(path, []byte("GGUFfixture"), 0600); err != nil {
		t.Fatal(err)
	}
	assets, warnings := Discover([]ModelRoot{{Path: root}}, nil)
	if len(warnings) != 0 || len(assets) != 1 || assets[0].Format != "gguf" || assets[0].Runtime != "" || assets[0].Complete || !assets[0].LoadCandidate() {
		t.Fatalf("assets %+v warnings %v", assets, warnings)
	}
	if err := os.WriteFile(path, []byte("bad!"), 0600); err != nil {
		t.Fatal(err)
	}
	assets, _ = Discover(nil, []string{path})
	if len(assets) != 1 || assets[0].LoadCandidate() {
		t.Fatalf("invalid GGUF admitted %+v", assets)
	}
}

func TestInstalledServersNoEndpointGuess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gufo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := InstalledServers()
	if !reflect.DeepEqual(got, []string{"gufo · " + path}) {
		t.Fatalf("installed %v", got)
	}
}

func TestDiscoveryGGUFNativeSnapshotAndExplicitFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models--org--model", "snapshots", "revision")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(file, []byte("GGUFfixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][]ModelRoot{{{Path: root, Kind: "hf"}}, {{Path: dir}}, {{Path: file}}} {
		got, warnings := Discover(roots, nil)
		if len(warnings) != 0 || len(got) != 1 || got[0].Path != canonical || !got[0].LoadCandidate() || got[0].Complete {
			t.Fatalf("got %+v warnings %v", got, warnings)
		}
	}
}

func TestInstalledRecipesUseConfiguredEndpointAndLiteralModel(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gufo", "llama-server"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	got := InstalledRecipes("http://127.0.0.1:18890/v1")
	if len(got) != 2 {
		t.Fatalf("recipes %+v", got)
	}
	for _, r := range got {
		want := []string{filepath.Join(dir, r.Name), "--host", "127.0.0.1", "--port", "18890", "--model", "{model}"}
		if r.Name == "gufo" {
			want = []string{filepath.Join(dir, r.Name), "serve", "--host", "127.0.0.1", "--port", "18890", "llm", "--model", "{model}"}
		}
		if !reflect.DeepEqual(r.Argv, want) {
			t.Fatalf("argv %v", r.Argv)
		}
		bound, err := BindModel(r, "/local/model with spaces.gguf")
		if err != nil || bound.Argv[len(bound.Argv)-1] != "/local/model with spaces.gguf" {
			t.Fatalf("binding %+v %v", bound, err)
		}
	}
	for _, endpoint := range []string{"http://example.com:8080/v1", "http://localhost/v1", "https://localhost:8080/v1", "http://localhost:8080/custom/v1"} {
		if len(InstalledRecipes(endpoint)) != 0 {
			t.Fatalf("invented launch flags/defaults for %s", endpoint)
		}
	}
}
