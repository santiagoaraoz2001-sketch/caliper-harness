package serve

import (
	"net/http"
	"runtime"
	"testing"
)

func TestRuntimeNativeDiscoveryRequiresCompleteModelShape(t *testing.T) {
	for _, tc := range []struct {
		body, want string
		status     int
	}{{`{"models":[]}`, "ollama", 200}, {`{"models":[{"name":"m"}]}`, "ollama", 200}, {`{}`, "", 200}, {`{"models":null}`, "", 200}, {`{"models":[]} trailing`, "", 200}, {`{"models":[]} {}`, "", 200}, {`bad`, "", 200}, {`{"models":[]}`, "", 503}} {
		client := &http.Client{Transport: &stubTransport{routes: map[string]struct {
			status int
			body   string
		}{"/api/ps": {tc.status, tc.body}}}}
		h := Probe(client, Endpoint{Kind: KindOllama, URL: "http://fixture.invalid/v1"})
		if h.Runtime != tc.want {
			t.Errorf("%s status%d runtime=%q want%q", tc.body, tc.status, h.Runtime, tc.want)
		}
	}
}
func TestRuntimeRecipesExplicitNoNameInference(t *testing.T) {
	r := DefaultRecipes("", "model", "")
	if len(r) == 0 || r[0].Runtime != "ollama" {
		t.Fatalf("builtins missing explicit metadata: %+v", r)
	}
	for _, rec := range r {
		if runtime.GOOS != "darwin" && rec.Runtime == "mlx" {
			t.Fatal("MLX exposed off Mac", rec)
		}
	}
	r = MergeRecipes(r, []Recipe{{Name: "ollama", Kind: KindOpenAI, URL: OllamaURL, Argv: []string{"custom"}}})
	if r[0].Runtime != "" {
		t.Fatal("same-name user recipe inherited builtin runtime")
	}
}
