package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitGGUFRequiresAllDeclaredHeaders(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "weights-00001-of-00002.gguf")
	two := filepath.Join(dir, "weights-00002-of-00002.gguf")
	os.WriteFile(one, []byte("GGUFfixture"), 0600)
	assets, _ := Discover(nil, []string{dir})
	if len(assets) != 1 || assets[0].LoadCandidate() || !strings.Contains(assets[0].Reason, "00002") {
		t.Fatal(assets)
	}
	os.WriteFile(two, []byte("GGUFfixture"), 0600)
	assets, warnings := Discover(nil, []string{dir})
	if len(assets) != 1 || !assets[0].LoadCandidate() || len(warnings) > 0 {
		t.Fatal(assets, warnings)
	}
	os.WriteFile(two, []byte("bad"), 0600)
	assets, _ = Discover(nil, []string{one})
	if assets[0].LoadCandidate() {
		t.Fatal("bad shard admitted")
	}
}
func TestLocalRecipeUsesRequestedEndpoint(t *testing.T) {
	r, ok := LocalRecipe("", "ollama", "http://127.0.0.1:19333", "m")
	if !ok || len(r.Env) != 1 || r.Env[0] != "OLLAMA_HOST=127.0.0.1:19333" {
		t.Fatal(r)
	}
	if _, ok := LocalRecipe("", "ollama", "https://remote.example/v1", "m"); ok {
		t.Fatal("remote launch offered")
	}
}
