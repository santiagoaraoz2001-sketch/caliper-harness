package config

import "testing"

func TestNativeJudgeAndEndpointOnlyRecipe(t *testing.T) {
	c := Defaults()
	c.Decision.Runtime = "system_one"
	c.Decision.Server = "http://localhost:8080/v1"
	c.Serve.Recipes = []RecipeConfig{{Name: "custom", Kind: "openai", URL: "http://localhost:8888/v1"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-a-url", "file:///tmp/server"} {
		c.Decision.Server = bad
		if err := c.Validate(); err == nil {
			t.Fatalf("decision URL accepted %q", bad)
		}
	}
}

func TestDuplicateServerRecipesRefused(t *testing.T) {
	c := Defaults()
	r := RecipeConfig{Name: "custom", Kind: "openai", URL: "http://localhost:8888/v1", Argv: []string{"server"}}
	c.Serve.Recipes = []RecipeConfig{r, r}
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate names accepted")
	}
}

func TestDecisionRejectsOrdinaryChatRuntimes(t *testing.T) {
	for _, runtime := range []string{"openai", "mlx", "ollama", "chat"} {
		c := Defaults()
		c.Decision.Runtime = runtime
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted ordinary chat judge %q", runtime)
		}
	}
}
