package serve

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubTransport fakes servers without sockets: path-routed bodies,
// or a dial-style error when fail is set.
type stubTransport struct {
	routes map[string]struct {
		status int
		body   string
	}
	fail bool
}

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if s.fail {
		return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: errors.New("connection refused")}
	}
	if route, ok := s.routes[r.URL.Path]; ok {
		return &http.Response{StatusCode: route.status, Body: io.NopCloser(strings.NewReader(route.body)), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}

func route(status int, body string) struct {
	status int
	body   string
} {
	return struct {
		status int
		body   string
	}{status, body}
}

func client(routes map[string]struct {
	status int
	body   string
}) *http.Client {
	return &http.Client{Transport: &stubTransport{routes: routes}, Timeout: 5 * time.Second}
}

func TestProbeOllamaUp(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{
		"/api/ps":   route(200, `{"models":[{"name":"qwen3:8b"},{"name":"x"}]}`),
		"/api/tags": route(200, `{"models":[{"name":"qwen3:8b"},{"name":"x"},{"name":"y"}]}`),
	})
	h := Probe(c, Endpoint{Kind: KindOllama, URL: "http://x:11434/v1"})
	if !h.Up || h.Kind != KindOllama || len(h.Loaded) != 2 || h.Loaded[0] != "qwen3:8b" {
		t.Errorf("health=%+v", h)
	}
	if len(h.Available) != 1 || h.Available[0] != "y" {
		t.Errorf("available=%v, want non-resident only", h.Available)
	}
	if h.LatencyMs < 0 {
		t.Errorf("negative latency %d", h.LatencyMs)
	}
}

func TestProbeOllamaTagsDown(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{"/api/ps": route(200, `{"models":[{"name":"m"}]}`)})
	h := Probe(c, Endpoint{Kind: KindOllama, URL: "http://x"})
	if !h.Up || len(h.Loaded) != 1 || len(h.Available) != 0 {
		t.Errorf("health=%+v, tags failure must not sink residency", h)
	}
}

func TestProbeDownIsLoud(t *testing.T) {
	c := &http.Client{Transport: &stubTransport{fail: true}, Timeout: 5 * time.Second}
	h := Probe(c, Endpoint{Kind: KindOllama, URL: "http://127.0.0.1:1/v1"})
	if h.Up || h.Err == "" || !strings.Contains(h.Err, "unreachable") {
		t.Errorf("health=%+v, want down+unreachable", h)
	}
}

func TestProbeRefused(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{"/api/ps": route(500, "")})
	h := Probe(c, Endpoint{Kind: KindOllama, URL: "http://x"})
	if h.Up || !strings.Contains(h.Err, "500") {
		t.Errorf("health=%+v, want HTTP 500", h)
	}
}

func TestProbeGarbage(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{"/v1/models": route(200, `not json`)})
	h := Probe(c, Endpoint{Kind: KindOpenAI, URL: "http://x"})
	if h.Up || !strings.Contains(h.Err, "garbage") {
		t.Errorf("health=%+v, want garbage", h)
	}
}

func TestProbeOpenAIUp(t *testing.T) {
	var gotPath string
	tr := &stubTransport{routes: map[string]struct {
		status int
		body   string
	}{"/v1/models": route(200, `{"data":[{"id":"mlx-community/x"}]}`)}}
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return tr.RoundTrip(r)
	}), Timeout: 5 * time.Second}
	h := Probe(c, Endpoint{Kind: KindOpenAI, URL: "http://x"})
	if !h.Up || len(h.Available) != 1 || h.Available[0] != "mlx-community/x" || len(h.Loaded) != 0 {
		t.Errorf("health=%+v", h)
	}
	if gotPath != "/v1/models" {
		t.Errorf("path=%s, want /v1/models", gotPath)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeSidecar(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{"/health": route(200, `{"pid":123,"status":"not_ready","loaded":false,"ready":false,"loading":false,"model":null,"active":null,"requested":null,"error":null,"backends":[]}`)})
	if h := Probe(c, Endpoint{Kind: KindSidecar, URL: "http://x:8090"}); !h.Up {
		t.Errorf("sidecar up=%+v", h)
	}
	d := client(map[string]struct {
		status int
		body   string
	}{"/health": route(502, "")})
	if h := Probe(d, Endpoint{Kind: KindSidecar, URL: "http://x:8090"}); h.Up {
		t.Errorf("sidecar down=%+v", h)
	}
}

func TestProbeAutoDetects(t *testing.T) {
	ollama := client(map[string]struct {
		status int
		body   string
	}{"/api/ps": route(200, `{"models":[]}`)})
	if h := Probe(ollama, Endpoint{Kind: KindAuto, URL: "http://x"}); !h.Up || h.Kind != KindOllama {
		t.Errorf("auto ollama=%+v", h)
	}
	plain := client(map[string]struct {
		status int
		body   string
	}{"/v1/models": route(200, `{"data":[]}`)})
	if h := Probe(plain, Endpoint{Kind: KindAuto, URL: "http://x"}); !h.Up || h.Kind != KindOpenAI {
		t.Errorf("auto openai=%+v", h)
	}
}

func TestProbeAutoDown(t *testing.T) {
	c := &http.Client{Transport: &stubTransport{fail: true}, Timeout: 5 * time.Second}
	h := Probe(c, Endpoint{Kind: KindAuto, URL: "http://127.0.0.1:1"})
	if h.Up {
		t.Errorf("auto down=%+v", h)
	}
}

func TestProbeAllKeepsOrder(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{
		"/api/ps": route(200, `{"models":[{"name":"m"}]}`),
		"/health": route(200, `{"pid":123,"status":"not_ready","loaded":false,"ready":false,"loading":false,"model":null,"active":null,"requested":null,"error":null,"backends":[]}`),
	})
	got := ProbeAll(c, []Endpoint{
		{Kind: KindOllama, URL: "http://a"},
		{Kind: KindSidecar, URL: "http://b"},
		{Kind: KindOpenAI, URL: "http://c"},
	})
	if len(got) != 3 || !got[0].Up || !got[1].Up || got[2].Up {
		t.Errorf("results=%+v", got)
	}
}

func TestStartRefuses(t *testing.T) {
	if _, err := Start(Recipe{Name: "blank"}, t.TempDir()+"/x.log"); err == nil {
		t.Error("blank argv must refuse")
	}
	if _, err := Start(Recipe{Name: "ghost", Argv: []string{"no-such-binary-xyz"}}, t.TempDir()+"/x.log"); err == nil {
		t.Error("missing binary must refuse")
	}
	if _, err := Start(Recipe{Name: "mlx", Argv: []string{"mlx_lm.server", "--model", "", "--port", "8080"}}, t.TempDir()+"/x.log"); err == nil {
		t.Error("model-less mlx must refuse")
	}
}

func TestStartStop(t *testing.T) {
	dir := t.TempDir()
	p, err := Start(Recipe{Name: "nap", Argv: []string{"sleep", "60"}}, filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("second stop: %v", err)
	}
	p, err = Start(Recipe{Name: "quick", Argv: []string{"true"}}, filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatalf("start true: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := p.Stop(); err != nil {
		t.Errorf("stop exited: %v", err)
	}
}

func TestRecipes(t *testing.T) {
	if got := DefaultRecipes("", "m", "typed-decisions"); len(got) != 2 {
		t.Errorf("no harness dir → %d recipes, want 2", len(got))
	}
	if got := DefaultRecipes("/h", "m", "english"); len(got) != 3 || !strings.Contains(got[2].Argv[1], "server.py") {
		t.Errorf("recipes=%+v", got)
	} else if got[2].Argv[len(got[2].Argv)-1] != "english" {
		t.Errorf("sidecar recipe must carry the decision model: %+v", got[2])
	}
	extra := []Recipe{{Name: "mlx", Kind: KindOpenAI, URL: MLXURL, Argv: []string{"custom"}}}
	merged := MergeRecipes(DefaultRecipes("/h", "m", "typed-decisions"), extra)
	if len(merged) != 3 || merged[1].Argv[0] != "custom" {
		t.Errorf("merged=%+v", merged)
	}
}

func TestLoopback(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8090":   true,
		"http://localhost:8090":   true,
		"http://192.168.1.5:8090": false,
		"http://example.com":      false,
		"::not a url::":           false,
	} {
		if got := Loopback(raw); got != want {
			t.Errorf("%s → %v, want %v", raw, got, want)
		}
	}
}

func TestFindHarnessDir(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "python", "decision")
	if err := os.MkdirAll(script, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(script, "server.py"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	got, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if FindHarnessDir() != got {
		t.Errorf("dir=%q, want %q", FindHarnessDir(), got)
	}
}

func TestProbeOllamaVram(t *testing.T) {
	c := client(map[string]struct {
		status int
		body   string
	}{
		"/api/ps":   route(200, `{"models":[{"name":"qwen3:8b","size_vram":10737418240},{"name":"bare"}]}`),
		"/api/tags": route(200, `{"models":[]}`),
	})
	h := Probe(c, Endpoint{Kind: KindOllama, URL: "http://x:11434/v1"})
	if h.Vram["qwen3:8b"] != 10737418240 {
		t.Errorf("vram=%v", h.Vram)
	}
	// Absent size_vram stays absent: unknown, never zero.
	if _, ok := h.Vram["bare"]; ok {
		t.Errorf("vram=%v", h.Vram)
	}
}
