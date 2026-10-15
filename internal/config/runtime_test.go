package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeSelectionYAML(t *testing.T) {
	for _, runtime := range []string{"openai", "ollama", "mlx"} {
		t.Run(runtime, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte("model:\n  runtime: "+runtime+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(p); err != nil {
				t.Fatalf("explicit runtime refused: %v", err)
			}
		})
	}
}
func TestRuntimeDefaultValidationAndSave(t *testing.T) {
	c := Defaults()
	if c.Model.Runtime != "openai" {
		t.Fatalf("default guesses runtime: %q", c.Model.Runtime)
	}
	for _, runtime := range []string{"ollama", "mlx", "openai"} {
		c.Model.Runtime = runtime
		p := filepath.Join(t.TempDir(), "runtime.yaml")
		if err := Save(p, c); err != nil {
			t.Fatal(err)
		}
		back, _, err := Load(p)
		if err != nil || back.Model.Runtime != runtime {
			t.Fatalf("runtime lost on reopen: %q %v", back.Model.Runtime, err)
		}
	}
	c.Model.Runtime = "auto"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid implicit runtime accepted")
	}
}
func TestRuntimeRecipeDescriptorPersistsWithoutNameInference(t *testing.T) {
	c := Defaults()
	c.Serve.Recipes = []RecipeConfig{{Name: "unrelated", Kind: "openai", Runtime: "mlx", URL: "http://fixture.invalid/v1", Argv: []string{"command"}}}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	back, _, err := Load(p)
	if err != nil || back.Serve.Recipes[0].Runtime != "mlx" {
		t.Fatalf("recipe descriptor lost: %+v %v", back.Serve, err)
	}
	c.Serve.Recipes[0].Runtime = "guess"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid recipe runtime accepted")
	}
}
