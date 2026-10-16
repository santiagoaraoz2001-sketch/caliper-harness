// Package serve probes the serving layer and manages its lifecycle:
// health/latency/loaded for worker runtimes and the decision sidecar,
// start-from-recipe, and stop-what-we-started. The TUI status screen
// renders this; main owns the sidecar auto-start/stop around the run.
package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"specific-harness/internal/bundle"
	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
)

// Endpoint kinds. Auto detects the worker shape (ollama, then OpenAI).
// Rulith is manager-driven (JSON stdio) with an OpenAI inference surface.
const (
	KindAuto    = "auto"
	KindOllama  = "ollama"
	KindOpenAI  = "openai"
	KindSidecar = "sidecar"
	KindRulith  = "rulith"
)

// Conventional base URLs the status screen always offers.
const (
	OllamaURL   = "http://127.0.0.1:11434/v1"
	MLXURL      = "http://127.0.0.1:8080/v1"
	LlamaURL    = "http://127.0.0.1:8080/v1" // llama-server default
	LMStudioURL = "http://127.0.0.1:1234/v1"
	SidecarURL  = "http://127.0.0.1:8090"
)

// ProbeTimeout bounds one endpoint check; ProbeAll runs them together.
const ProbeTimeout = 3 * time.Second

// Endpoint is one servable thing: a worker runtime or the sidecar.
type Endpoint struct {
	Kind  string // auto, ollama, openai, sidecar
	Label string // worker, ollama, mlx, sidecar
	URL   string // worker: OpenAI base (.../v1); sidecar: bare base
	Model string // wanted model id (worker rows)
}

// Health is one probe result. Err is plain language, set only when down.
type Health struct {
	APIBase string // exact validated worker API base for subsequent inference

	Runtime      string // healthy native discovery; generic compatibility does not prove MLX
	Reachable    bool   // an HTTP answer prevents starting a second server here
	Decision     *decision.Health
	Up           bool
	Kind         string // detected kind (auto resolves here)
	LatencyMs    int64
	Loaded       []string // resident in memory (ollama /api/ps)
	InventoryErr string   // catalog failure remains distinct from proven residency
	Available    []string // known to the server (ollama /api/tags, openai /v1/models)
	// Vram maps resident model name → bytes in VRAM (ollama /api/ps
	// size_vram; other servers leave it empty = unknown, not zero).
	Vram map[string]uint64
	Err  string
}

// Probe checks one endpoint: sidecar /health, ollama /api/ps (loaded
// models), OpenAI /v1/models. Non-200, bad JSON, and unreachable all
// read as down with the reason — never an error return.
func Probe(client *http.Client, e Endpoint) Health {
	return ProbeContext(context.Background(), client, e)
}

// ProbeContext gives startup cancellation the same health semantics as Probe.
func ProbeContext(ctx context.Context, client *http.Client, e Endpoint) Health {
	kind := e.Kind
	if kind == KindAuto {
		// Only a missing native route (404) tries the OpenAI shape. A sick
		// ollama (500, garbage) stays labeled ollama.
		if h := probeOllamaContext(ctx, client, e.URL); h.Up || !tryNext(h.Err) {
			h.Kind = KindOllama
			return h
		}
		h := probeOpenAIContext(ctx, client, e.URL)
		h.Kind = KindOpenAI
		return h
	}
	switch kind {
	case KindOllama:
		h := probeOllamaContext(ctx, client, e.URL)
		h.Kind = KindOllama
		return h
	case KindRulith:
		h := probeRulithContext(ctx, client, e.URL)
		h.Kind = KindRulith
		return h
	case KindSidecar:
		h := probeSidecarContext(ctx, client, e.URL)
		h.Kind = KindSidecar
		return h
	default:
		h := probeOpenAIContext(ctx, client, e.URL)
		h.Kind = KindOpenAI
		return h
	}
}

// tryNext reports whether auto-detection should try the next shape:
// only an explicit missing-route answer tries a different shape.
func tryNext(err string) bool {
	return strings.Contains(err, "HTTP 404")
}

func root(u string) string {
	return strings.TrimSuffix(strings.TrimRight(u, "/"), "/v1")
}

func timed(client *http.Client, target string) (int64, *http.Response, string) {
	return timedContext(context.Background(), client, target)
}
func timedContext(ctx context.Context, client *http.Client, target string) (int64, *http.Response, string) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Sprintf("unreachable: %v — is it running?", shortErr(err))
	}
	return time.Since(start).Milliseconds(), resp, ""
}

func shortErr(err error) error {
	if uerr, ok := err.(*url.Error); ok {
		return uerr.Err
	}
	return err
}

func probeSidecar(client *http.Client, base string) Health {
	return probeSidecarContext(context.Background(), client, base)
}
func probeSidecarContext(ctx context.Context, client *http.Client, base string) Health {
	ms, resp, errStr := timedContext(ctx, client, strings.TrimRight(base, "/")+"/health")
	if resp == nil {
		return Health{Err: errStr}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{Reachable: true, Err: fmt.Sprintf("sidecar refused: HTTP %d", resp.StatusCode)}
	}
	h, err := decision.DecodeHealth(resp.Body)
	if err != nil {
		return Health{Reachable: true, Err: err.Error()}
	}
	return Health{Reachable: true, Up: true, LatencyMs: ms, Decision: &h}
}

func probeOllama(client *http.Client, base string) Health {
	return probeOllamaContext(context.Background(), client, base)
}

func probeOllamaContext(ctx context.Context, client *http.Client, base string) Health {
	ms, resp, errStr := timedContext(ctx, client, root(base)+"/api/ps")
	if resp == nil {
		return Health{Err: errStr}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{Reachable: true, Err: fmt.Sprintf("ollama refused: HTTP %d", resp.StatusCode)}
	}
	type residentModel struct {
		Name     string `json:"name"`
		SizeVRAM uint64 `json:"size_vram"`
	}
	var list struct {
		Models *[]residentModel `json:"models"`
	}
	if err := decodeWhole(resp.Body, &list); err != nil || list.Models == nil {
		return Health{Reachable: true, Err: fmt.Sprintf("ollama answered garbage/incomplete inventory: %v", err)}
	}
	h := Health{APIBase: root(base) + "/v1", Reachable: true, Up: true, Runtime: "ollama", LatencyMs: ms, Vram: map[string]uint64{}}
	var models []residentModel
	if list.Models != nil {
		models = *list.Models
	}
	for _, m := range models {
		if strings.TrimSpace(m.Name) == "" {
			return Health{Reachable: true, Err: "ollama answered an empty model identity"}
		}
		h.Loaded = append(h.Loaded, m.Name)
		// Absent size_vram stays absent: unknown, never zero.
		if m.SizeVRAM > 0 {
			h.Vram[m.Name] = m.SizeVRAM
		}
	}
	names, tagErr := ollamaTags(ctx, client, base)
	if tagErr != nil {
		h.InventoryErr = tagErr.Error()
	}
	for _, name := range names {
		dup := false
		for _, l := range h.Loaded {
			if l == name {
				dup = true
			}
		}
		if !dup {
			h.Available = append(h.Available, name)
		}
	}
	return h
}

// ollamaTags requires a complete same-server installed catalog.
func ollamaTags(ctx context.Context, client *http.Client, base string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root(base)+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama catalog refused: HTTP %d", resp.StatusCode)
	}
	var list struct {
		Models *[]struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := decodeWhole(resp.Body, &list); err != nil || list.Models == nil {
		return nil, fmt.Errorf("invalid ollama catalog: %v", err)
	}
	var out []string
	for _, m := range *list.Models {
		if strings.TrimSpace(m.Name) == "" {
			return nil, fmt.Errorf("empty ollama catalog identity")
		}
		out = append(out, m.Name)
	}
	return out, nil
}

// decodeWhole bounds metadata/health bodies and refuses trailing garbage.
func decodeWhole(r io.Reader, out any) error {
	b, err := io.ReadAll(io.LimitReader(r, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 1024*1024 {
		return fmt.Errorf("metadata exceeds 1MiB bound")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing metadata: %v", err)
	}
	return nil
}
func probeOpenAI(client *http.Client, base string) Health {
	return probeOpenAIContext(context.Background(), client, base)
}
func probeOpenAIContext(ctx context.Context, client *http.Client, base string) Health {
	base = strings.TrimRight(base, "/")
	if u, err := url.Parse(base); err == nil && u.Path == "" {
		base += "/v1"
	}
	ms, resp, errStr := timedContext(ctx, client, base+"/models")
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		if strings.HasSuffix(base, "/v1") {
			base = root(base)
		} else {
			base += "/v1"
		}
		ms, resp, errStr = timedContext(ctx, client, base+"/models")
	}
	if resp == nil {
		return Health{Err: errStr}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{Reachable: true, Err: fmt.Sprintf("server refused: HTTP %d", resp.StatusCode)}
	}
	var list struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decodeWhole(resp.Body, &list); err != nil || list.Data == nil {
		return Health{Reachable: true, Err: fmt.Sprintf("server answered garbage/incomplete inventory: %v", err)}
	}
	h := Health{APIBase: base, Reachable: true, Up: true, LatencyMs: ms}
	for _, m := range *list.Data {
		if strings.TrimSpace(m.ID) == "" {
			return Health{Reachable: true, Err: "server answered an empty model identity"}
		}
		h.Available = append(h.Available, m.ID)
	}
	return h
}

// ProbeAll checks every endpoint concurrently, keeping table order.
// One slow endpoint never holds the rest past its own timeout.
func ProbeAll(client *http.Client, endpoints []Endpoint) []Health {
	out := make([]Health, len(endpoints))
	var wg sync.WaitGroup
	for i, e := range endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = Probe(client, e)
		}()
	}
	wg.Wait()
	return out
}

// Recipe starts one server: argv plus its working directory.
type Recipe struct {
	Runtime       string // explicit worker wire contract, independent of display name
	Name          string
	Kind          string
	URL           string // endpoint this recipe serves
	Argv          []string
	Dir           string
	Env           []string // additional explicit process environment
	localPath     string   // prepared native local worker only
	managedPython bool     // built-in private Mac interpreter only
}

// SidecarRecipe builds the recipe for one sidecar URL, reading the
// port from the URL (8090 when it names none). ok is false when the
// repo (the server script) cannot be found.
func SidecarRecipe(harnessDir, serverURL, model string, selected ...decision.Selection) (r Recipe, ok bool) {
	if harnessDir == "" {
		return Recipe{}, false
	}
	port := "8090"
	if u, err := url.Parse(serverURL); err == nil && u.Port() != "" {
		port = u.Port()
	}
	s := decision.Selection{Model: model, Runtime: "laya", Device: "auto"}
	if len(selected) > 0 {
		s = selected[0]
	}
	python := "python3"
	managed := bundle.Managed(harnessDir)
	if managed {
		python = bundle.Python(harnessDir, "decision")
	}
	argv := []string{python, filepath.Join(harnessDir, "python", "decision", "server.py"), "--port", port, "--runtime", s.Runtime, "--device", s.Device}
	if s.Path != nil {
		argv = append(argv, "--model-path", *s.Path)
	}
	if s.Revision != nil {
		argv = append(argv, "--revision", *s.Revision)
	}
	if s.TrustRemoteCode {
		argv = append(argv, "--trust-remote-code")
	}
	argv = append(argv, "--model", s.Model)
	return Recipe{Name: "sidecar", Kind: KindSidecar, URL: serverURL,
		Argv: argv,
		Dir:  harnessDir, managedPython: managed}, true
}

// SidecarPrompts binds an owned Python recipe to the same admitted YAML.
// Custom recipes keep their literal argv and are not passed through this owner.
func SidecarPrompts(r Recipe, c *prompts.Catalog) (Recipe, error) {
	if c == nil || c.Source == "embedded prompts.yaml" {
		return r, fmt.Errorf("decision startup requires the shared prompts.yaml asset; the Go catalog is embedded")
	}
	path, err := filepath.Abs(c.Source)
	if err != nil {
		return r, err
	}
	if _, err := os.Stat(path); err != nil {
		return r, fmt.Errorf("decision prompt catalog %s: %w", path, err)
	}
	r.Argv = append(append([]string(nil), r.Argv...), "--prompts-file", path)
	return r, nil
}

// DefaultRecipes covers only what Caliper itself owns: platform driver
// recipes (the owned MLX worker on darwin; the Rulith manager handle on
// windows) plus the decision sidecar. Model servers in general are NOT
// hardcoded here: they are discovered live (see DiscoverServers),
// relaunched from their captured command lines, or registered by the
// user in serve.recipes. harnessDir locates the sidecar script; blank
// means no sidecar recipe.
func DefaultRecipes(harnessDir, workerModel, decisionModel string, selected ...decision.Selection) []Recipe {
	out := driverRecipes(harnessDir, workerModel)
	if r, ok := SidecarRecipe(harnessDir, SidecarURL, decisionModel, selected...); ok {
		out = append(out, r)
	}
	return out
}

// MergeRecipes appends user recipes, with a same-name user recipe
// replacing its built-in.
func MergeRecipes(builtIn []Recipe, extra []Recipe) []Recipe {
	out := append([]Recipe(nil), builtIn...)
	for _, r := range extra {
		replaced := false
		for i, b := range out {
			if b.Name == r.Name {
				out[i] = r
				replaced = true
			}
		}
		if !replaced {
			out = append(out, r)
		}
	}
	return out
}

// FindHarnessDir locates the repo (the sidecar script) from the cwd,
// else the executable's directory. Blank means no recipe.
func FindHarnessDir() string {
	if root := bundle.Root(); root != "" {
		return root
	}
	const script = "python/decision/server.py"
	if _, err := os.Stat(script); err == nil {
		if abs, err := filepath.Abs("."); err == nil {
			return abs
		}
		return "."
	}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" {
			if _, err := os.Stat(filepath.Join(dir, script)); err == nil {
				return dir
			}
		}
	}
	return ""
}

// Proc is one recipe we started; only these may be stopped by us.
type Proc struct {
	cmd            *exec.Cmd
	done           chan struct{}
	localPath, url string
}

// Start launches a recipe detached, logging to logPath. Blank argv,
// a missing binary, and (for mlx) a missing model refuse loudly.
func Start(r Recipe, logPath string, probeTimeout ...time.Duration) (*Proc, error) {
	return StartContext(context.Background(), r, logPath, probeTimeout...)
}

// StartContext cancels admission; a settled resident child keeps its own lifetime.
func StartContext(ctx context.Context, r Recipe, logPath string, probeTimeout ...time.Duration) (*Proc, error) {
	timeout := ProbeTimeout
	if len(probeTimeout) > 0 {
		timeout = probeTimeout[0]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.Argv) == 0 {
		return nil, fmt.Errorf("recipe %q has nothing to run", r.Name)
	}
	if r.Name == "mlx" {
		for i, a := range r.Argv {
			if a == "--model" && (i+1 >= len(r.Argv) || r.Argv[i+1] == "") {
				return nil, fmt.Errorf("mlx needs a model — set model.name first")
			}
		}
	}
	if _, err := exec.LookPath(r.Argv[0]); err != nil {
		return nil, fmt.Errorf("recipe %q needs %q on PATH: %v", r.Name, r.Argv[0], err)
	}
	if r.localPath != "" {
		if !Loopback(r.URL) || probeOpenAIContext(ctx, &http.Client{Timeout: timeout}, r.URL).Reachable {
			return nil, fmt.Errorf("owned MLX requires a free loopback endpoint")
		}
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("recipe %q cannot open log %s: %v", r.Name, logPath, err)
	}
	defer f.Close()
	cmd := exec.Command(r.Argv[0], r.Argv[1:]...)
	cmd.Env = append(os.Environ(), r.Env...)
	if r.managedPython {
		cmd.Env = bundle.PythonEnv(os.Environ(), r.Env)
	}
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("recipe %q failed to start: %v", r.Name, err)
	}
	p := &Proc{cmd: cmd, done: make(chan struct{}), localPath: r.localPath, url: r.URL}
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// PID returns only a still-owned, unreaped process identity.
func (p *Proc) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.done == nil {
		return 0
	}
	select {
	case <-p.done:
		return 0
	default:
		return p.cmd.Process.Pid
	}
}

// Stop kills our process. Already-exited reads as stopped, not an error.
func (p *Proc) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
		return fmt.Errorf("could not stop: %v", err)
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("owned process did not settle within 5s")
		}
	}
	return nil
}

// Loopback reports whether the URL points at this machine — only those
// endpoints are ours to auto-start.
func Loopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
