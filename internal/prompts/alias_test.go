package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCheckoutAliasesUseCanonicalCatalog(t *testing.T) {
	for _, path := range []string{"../../prompts.yaml", "../../templates/prompts.yaml"} {
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.YAML() != Builtin().YAML() {
			t.Fatalf("catalog drift: %s", path)
		}
	}
	p := filepath.Join(t.TempDir(), "prompts.yaml")
	os.WriteFile(p, []byte("elsewhere.yaml"), 0600)
	if _, err := Load(p); err == nil {
		t.Fatal("accepted arbitrary include")
	}
}
