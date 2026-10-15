package serve

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"specific-harness/internal/prompts"
)

func TestOwnedDecisionRecipeUsesAdmittedCatalogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edited prompts.yaml")
	if err := os.WriteFile(path, []byte(prompts.Builtin().YAML()), 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := prompts.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := SidecarRecipe("/application", "http://127.0.0.1:8090", "open/model")
	if !ok {
		t.Fatal("recipe absent")
	}
	before := append([]string(nil), r.Argv...)
	bound, err := SidecarPrompts(r, pc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bound.Argv, append(before, "--prompts-file", path)) || !reflect.DeepEqual(r.Argv, before) {
		t.Fatalf("bound=%q original=%q", bound.Argv, r.Argv)
	}
	if _, err := SidecarPrompts(r, prompts.Builtin()); err == nil {
		t.Fatal("embedded source treated as Python file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := SidecarPrompts(r, pc); err == nil {
		t.Fatal("deleted shared asset admitted for startup")
	}
}
