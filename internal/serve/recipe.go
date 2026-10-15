package serve

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Executable reports installation independently from endpoint health.
func (r Recipe) Executable() (string, error) {
	if len(r.Argv) == 0 {
		return "", fmt.Errorf("external endpoint; no launch command configured")
	}
	return exec.LookPath(r.Argv[0])
}

// BindModel substitutes only explicit placeholders within literal argv elements.
// It never invokes a shell or guesses a server's command-line syntax.
func BindModel(r Recipe, model string) (Recipe, error) {
	r.Argv = append([]string(nil), r.Argv...)
	for i, arg := range r.Argv {
		if strings.Contains(arg, "{model}") {
			if strings.TrimSpace(model) == "" {
				return Recipe{}, fmt.Errorf("recipe %q requires a selected model", r.Name)
			}
			r.Argv[i] = strings.ReplaceAll(arg, "{model}", model)
		}
	}
	return r, nil
}

func (r Recipe) HasModelPlaceholder() bool {
	for _, a := range r.Argv {
		if strings.Contains(a, "{model}") {
			return true
		}
	}
	return false
}

// InstalledServers finds known local launchers without guessing their ports or
// starting them. Presence is installation evidence, not a running endpoint.
func InstalledServers() []string {
	var found []string
	for _, name := range []string{"ollama", "mlx_lm.server", "gufo", "llama-server"} {
		if path, err := exec.LookPath(name); err == nil {
			found = append(found, name+" · "+path)
		}
	}
	return found
}

// InstalledRecipes offers explicit launch choices on the user's existing endpoint.
// Known servers expose root /v1; custom prefixes and unconfigured ports need a recipe.
func InstalledRecipes(endpoint string) []Recipe {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || !Loopback(endpoint) || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (strings.TrimRight(u.Path, "/") != "" && strings.TrimRight(u.Path, "/") != "/v1") {
		return nil
	}
	var out []Recipe
	for _, name := range []string{"gufo", "llama-server"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		args := []string{executable, "--host", u.Hostname(), "--port", u.Port(), "--model", "{model}"}
		if name == "gufo" {
			args = []string{executable, "serve", "--host", u.Hostname(), "--port", u.Port(), "llm", "--model", "{model}"}
		}
		out = append(out, Recipe{Name: name, Kind: KindOpenAI, Runtime: "openai", URL: endpoint, Argv: args, Discovered: true})
	}
	return out
}
