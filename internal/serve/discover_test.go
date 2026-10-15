package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeAsset(t *testing.T, p string, index bool) {
	t.Helper()
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"config.json": `{"model_type":"unrecognized-architecture-is-only-candidate"}`, "tokenizer.json": `{}`, "model.safetensors": "component weight stat only"}
	if index {
		delete(files, "model.safetensors")
		files["model.safetensors.index.json"] = `{"weight_map":{"x":"part.safetensors"}}`
		files["part.safetensors"] = "stat only"
	}
	for k, v := range files {
		if err := os.WriteFile(filepath.Join(p, k), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestD09DiscoveryPinnedLocalAndNoActivation(t *testing.T) {
	root := t.TempDir()
	snapshot := filepath.Join(root, "models--org--model", "snapshots", "not-main-revision")
	makeAsset(t, snapshot, true)
	standalone := filepath.Join(t.TempDir(), "standalone")
	makeAsset(t, standalone, false)
	broken := filepath.Join(root, "models--org--broken", "snapshots", "revision")
	makeAsset(t, broken, true)
	os.Remove(filepath.Join(broken, "part.safetensors"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(snapshot, alias); err != nil {
		t.Fatal(err)
	}
	catalog := t.TempDir()
	assets, warn := Discover([]ModelRoot{{root, "hf"}, {catalog, "ollama"}, {filepath.Join(root, "absent"), "hf"}}, []string{standalone, alias})
	if len(warn) != 0 || len(assets) != 4 {
		t.Fatalf("assets%+v warnings%v", assets, warn)
	}
	seen := map[string]Asset{}
	for _, a := range assets {
		seen[a.Path] = a
		if a.Complete && !strings.Contains(a.Reason, "compatibility unconfirmed") {
			t.Fatal("metadata called compatible")
		}
	}
	snapshot, _ = filepath.EvalSymlinks(snapshot)
	broken, _ = filepath.EvalSymlinks(broken)
	catalog, _ = filepath.EvalSymlinks(catalog)
	if a := seen[snapshot]; !a.Complete || a.ID != "org/model" || a.Revision != "not-main-revision" {
		t.Fatalf("snapshot %+v", a)
	}
	if seen[broken].Complete || !strings.Contains(seen[broken].Reason, "shard") {
		t.Fatalf("incomplete %+v", seen[broken])
	}
	if seen[catalog].Complete || !strings.Contains(seen[catalog].Reason, "server") {
		t.Fatal("native directory invented resident IDs")
	}
}
func TestD09DiscoveryRefusesEscapingShardAndMalformedMetadata(t *testing.T) {
	p := t.TempDir()
	makeAsset(t, p, true)
	os.WriteFile(filepath.Join(p, "model.safetensors.index.json"), []byte(`{"weight_map":{"x":"../escape"}}`), 0600)
	a, w := Discover(nil, []string{p})
	if len(w) != 0 || len(a) != 1 || a[0].Complete || !strings.Contains(a[0].Reason, "escapes") {
		t.Fatalf("%+v %v", a, w)
	}
	os.WriteFile(filepath.Join(p, "config.json"), []byte(`{} trailing`), 0600)
	a, _ = Discover(nil, []string{p})
	if a[0].Complete || !strings.Contains(a[0].Reason, "config") {
		t.Fatal("malformed config accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("not directory"), 0600)
	_, w = Discover([]ModelRoot{{file, "hf"}}, nil)
	if len(w) == 0 {
		t.Fatal("inaccessible/notdirectory root silently absent")
	}
}
func TestD09RootOverridePrecedence(t *testing.T) {
	for _, k := range []string{"HF_HUB_CACHE", "HUGGINGFACE_HUB_CACHE", "HF_HOME", "XDG_CACHE_HOME", "OLLAMA_MODELS"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	if ModelRoots("/home")[0].Path != "/xdg/huggingface/hub" {
		t.Fatal("xdg")
	}
	t.Setenv("HF_HOME", "/hf")
	t.Setenv("HUGGINGFACE_HUB_CACHE", "/legacy")
	t.Setenv("HF_HUB_CACHE", "/primary")
	t.Setenv("OLLAMA_MODELS", "/native")
	r := ModelRoots("/home")
	if r[0].Path != "/primary" || r[1].Path != "/native" {
		t.Fatal(r)
	}
}
