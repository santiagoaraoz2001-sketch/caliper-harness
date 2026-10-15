package tui

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/hw"
	"specific-harness/internal/serve"
)

// pathStub fakes servers by path without sockets.
type pathStub struct {
	routes map[string]struct {
		status int
		body   string
	}
}

func (s *pathStub) RoundTrip(r *http.Request) (*http.Response, error) {
	if route, ok := s.routes[r.URL.Path]; ok {
		return &http.Response{StatusCode: route.status, Body: io.NopCloser(strings.NewReader(route.body)), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}

func probeClient() *http.Client {
	return &http.Client{Transport: &pathStub{routes: map[string]struct {
		status int
		body   string
	}{
		"/api/ps":   {200, `{"models":[{"name":"qwen3:8b"}]}`},
		"/api/tags": {200, `{"models":[{"name":"qwen3:8b"},{"name":"llama3"}]}`},
		"/health":   {200, `{"pid":123,"status":"not_ready","loaded":false,"ready":false,"loading":false,"model":null,"active":null,"requested":null,"error":null,"backends":[]}`},
	}}}
}

// serveModelWithModels is serveModel plus resident + available rows
// under the ollama server (indexes 2,3; sidecar moves to 4).
func serveModelWithModels(t *testing.T) Model {
	t.Helper()
	m := serveModel(t)
	ollama := m.Serve.Rows[1]
	mk := func(name string, resident bool) ServeRow {
		return ServeRow{
			Ep:     serve.Endpoint{Kind: serve.KindOllama, Label: ollama.Ep.Label, URL: ollama.Ep.URL, Model: name},
			Health: serve.Health{Up: true, Kind: serve.KindOllama},
			Have:   true, IsModel: true, Resident: resident,
		}
	}
	m.Serve.Rows = []ServeRow{
		m.Serve.Rows[0], ollama,
		mk("qwen3:8b", true), mk("llama3", false),
		m.Serve.Rows[2],
	}
	return m
}

func TestServeModelRowsBuilt(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Isolate optional installed-server choices.
	isolateModelRoots(t)
	m := submit(New(testCfg(), "test"), "/status")
	msg := m.probeServeWith(probeClient())()
	probed, ok := msg.(serveProbedMsg)
	if !ok {
		t.Fatalf("msg=%T", msg)
	}
	var labels []string
	for _, r := range probed.rows {
		if r.IsModel {
			labels = append(labels, (map[bool]string{true: "R:", false: "A:"})[r.Resident]+r.Ep.Model)
		} else {
			labels = append(labels, "S:"+r.Ep.Label)
		}
	}
	want := []string{"S:worker", "R:qwen3:8b", "A:llama3", "S:ollama", "R:qwen3:8b", "A:llama3", "S:sidecar"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("rows=%v", labels)
	}
}

func TestServeModelUse(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 2
	m = key(m, "u")
	if m.Cfg.Model.Server != serve.OllamaURL || m.Cfg.Model.Name != "qwen3:8b" || m.ModelName != "qwen3:8b" {
		t.Errorf("server=%q name=%q", m.Cfg.Model.Server, m.Cfg.Model.Name)
	}
	if a := lastAlert(m); !strings.Contains(a.Text, "runtime: qwen3:8b on ollama") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeModelUnloadOne(t *testing.T) {
	m, calls := boundModelFixture(t)
	m.Serve.Sel = 2
	m = executeServeKey(t, m, "x")
	if !strings.Contains(lastAlert(m).Text, "resident action confirmed") {
		t.Fatal(lastAlert(m))
	}
	if got := calls(); got != "qwen3:8b:false" {
		t.Fatal(got)
	}
}
func TestServeModelUnloadAvailable(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 3
	m = key(m, "x")
	if !strings.Contains(lastAlert(m).Text, "not resident") {
		t.Fatal(lastAlert(m))
	}
}
func TestServeModelResidentLoad(t *testing.T) {
	m, calls := boundModelFixture(t)
	m.Serve.Sel = 3
	m = executeServeKey(t, m, "l")
	if m.Serve.Loading != "" || calls() != "llama3:true" {
		t.Fatal("installed load not settled", calls())
	}
}
func TestServeModelEnter(t *testing.T) {
	m, calls := boundModelFixture(t)
	m.Serve.Sel = 2
	m = executeServeKey(t, m, "enter")
	if calls() != "qwen3:8b:false" {
		t.Fatal(calls())
	}
	m.Serve.Sel = 3
	m = executeServeKey(t, m, "enter")
	if calls() != "qwen3:8b:false,llama3:true" {
		t.Fatal(calls())
	}
}

func TestServeModelRender(t *testing.T) {
	m := serveModelWithModels(t)
	m.Cfg.Model.Server = serve.OllamaURL
	m.Cfg.Model.Name = "qwen3:8b"
	m.Width = 160
	got := viewportServePages(m, m.Width, 12)
	for _, want := range []string{"├─", "└─", "resident — x unload", "available — l load", "[requested next]"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen omits %q", want)
		}
	}
}

func TestServeKeepModelSelection(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 3
	m.onServeProbed(serveProbedMsg{rows: m.Serve.Rows})
	if m.Serve.Sel != 3 {
		t.Errorf("sel=%d, want 3", m.Serve.Sel)
	}
}

// repoRoot puts the test where FindHarnessDir resolves the sidecar script.
func repoRoot(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
}

func TestServeSidecarModelEditor(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m = key(m, "m")
	if m.Settings.Editing == nil || m.Settings.Editing.Key != "model" {
		t.Fatal("model editor not armed")
	}
	m.applyEdit("org/compatible-checkpoint")
	if m.Cfg.Decision.Model != "typed-decisions" || m.Settings.DecisionDraft.Model != "org/compatible-checkpoint" {
		t.Fatal("selection not staged")
	}
	m.saveSettings()
	if a := lastAlert(m); !strings.Contains(a.Text, "pending external activation") {
		t.Fatal(a.Text)
	}
	loaded, _, err := config.Load(m.CfgPath)
	if err != nil || loaded.Decision.Model != "org/compatible-checkpoint" {
		t.Fatalf("save requested: %v", err)
	}
}

func TestRecipeForFresh(t *testing.T) {
	repoRoot(t)
	m := serveModel(t)
	m.Cfg.Decision.Model = "english"
	stale := serve.Recipe{Name: "sidecar", Kind: serve.KindSidecar, URL: m.Cfg.Decision.Server, Argv: []string{"stale"}}
	m.Serve.Rows[2].Recipe = &stale
	rec := m.recipeFor(&m.Serve.Rows[2])
	if rec == nil || rec.Argv[len(rec.Argv)-1] != "english" {
		t.Errorf("sidecar recipe must carry the current model: %+v", rec)
	}
	plain := m.Serve.Rows[1]
	plain.Recipe = &stale
	if got := m.recipeFor(&plain); got != &stale {
		t.Errorf("server rows keep the cached recipe: %+v", got)
	}
}

func TestDecisionUnprovenOwnerIsNeverStopped(t *testing.T) {
	m := serveModel(t)
	old, err := serve.Start(serve.Recipe{Name: "nap", Argv: []string{"sleep", "60"}}, filepath.Join(t.TempDir(), "x.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Stop()
	url := m.Cfg.Decision.Server
	m.Serve.Spawned = map[string]*serve.Proc{url: old}
	m.applyField(SettingField{"decision", "model", "decision model", "text"}, "other-compatible")
	if cmd := m.applyDecisionSelection(); cmd != nil {
		t.Fatal("unproven owner may not activate")
	}
	if old.PID() == 0 || m.Serve.Spawned[url] != old {
		t.Fatal("good active process discarded")
	}
	if !strings.Contains(m.Settings.DecisionNotice, "pending external") {
		t.Fatal(m.Settings.DecisionNotice)
	}
}

// serveModel returns a model on the status screen with fabricated
// probe results — no sockets. rows: worker up, ollama up, sidecar down.
func serveModel(t *testing.T) Model {
	t.Helper()
	m := submit(New(testCfg(), "test"), "/status")
	m.CfgPath = filepath.Join(t.TempDir(), "specific-harness.yaml")
	m.Serve.Rows = []ServeRow{
		{Ep: serve.Endpoint{Kind: serve.KindAuto, Label: "worker", URL: "http://127.0.0.1:8080/v1", Model: "m"},
			Health: serve.Health{Up: true, Kind: serve.KindOpenAI, LatencyMs: 3}, Have: true, Worker: true},
		{Ep: serve.Endpoint{Kind: serve.KindOllama, Label: "ollama", URL: serve.OllamaURL},
			Health: serve.Health{Up: true, Kind: serve.KindOllama, LatencyMs: 4, Loaded: []string{"qwen3:8b"}}, Have: true},
		{Ep: serve.Endpoint{Kind: serve.KindSidecar, Label: "sidecar", URL: "http://127.0.0.1:8090", Model: "typed-decisions"},
			Health: serve.Health{Up: false, Kind: serve.KindSidecar, Err: "unreachable: refused"}, Have: true, Sidecar: true},
	}
	return m
}

func lastAlert(m Model) Alert { return m.Alerts[len(m.Alerts)-1] }

func TestServeTable(t *testing.T) {
	m := New(testCfg(), "test")
	eps := m.serveEndpoints()
	if len(eps) != 3 || eps[0].Label != "worker" || eps[1].Label != "ollama" || eps[2].Label != "sidecar" {
		t.Errorf("endpoints=%+v", eps)
	}
	m.Cfg.Model.Server = "http://127.0.0.1:9999/v1"
	if got := len(m.serveEndpoints()); got != 4 {
		t.Errorf("custom server → %d rows, want 4", got)
	}
}

func TestServeUseSwitches(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 1
	m = key(m, "u")
	if m.Cfg.Model.Server != serve.OllamaURL || m.Server != serve.OllamaURL {
		t.Errorf("server=%q cfg=%q", m.Server, m.Cfg.Model.Server)
	}
	raw, err := os.ReadFile(m.CfgPath)
	if err != nil || !strings.Contains(string(raw), serve.OllamaURL) {
		t.Errorf("yaml missing new runtime: %v", err)
	}
	if a := lastAlert(m); !strings.Contains(a.Text, "runtime: ollama") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeUseDownRefuses(t *testing.T) {
	m := serveModel(t)
	m.Serve.Rows[1].Health = serve.Health{Up: false, Err: "unreachable: down"}
	m.Serve.Sel = 1
	before := m.Cfg.Model.Server
	m = key(m, "u")
	if m.Cfg.Model.Server != before {
		t.Error("down runtime must not switch")
	}
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "is down") {
		t.Errorf("alert=%+v", a)
	}
	if _, err := os.Stat(m.CfgPath); !os.IsNotExist(err) {
		t.Error("refused switch must not write")
	}
}

func TestServeUseNeedsConfig(t *testing.T) {
	m := serveModel(t)
	m.CfgPath = ""
	m.Serve.Sel = 1
	m = key(m, "u")
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "--config") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeUseRejectsSidecar(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m.Serve.Rows[2].Health = serve.Health{Up: true, Kind: serve.KindSidecar}
	m = key(m, "u")
	if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeDeviceCycles(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	for _, want := range []string{"cpu", "gpu", "auto"} {
		m = key(m, "d")
		if m.Cfg.Decision.Device != "auto" || m.Settings.DecisionDraft.Device != want {
			t.Fatalf("device must stage %q", want)
		}
	}
	m.saveSettings()
	raw, err := os.ReadFile(m.CfgPath)
	if err != nil || !strings.Contains(string(raw), "device: auto") {
		t.Errorf("yaml missing device: %v", err)
	}
	m.Serve.Sel = 0
	m = key(m, "d")
	if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("non-sidecar device must err: %+v", a)
	}
}

func TestServeStartGuards(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 0
	m = key(m, "s")
	if a := lastAlert(m); !strings.Contains(a.Text, "already up") {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Sel = 2
	m = key(m, "s")
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "no start recipe") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeUnloadGuards(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m = key(m, "x")
	if a := lastAlert(m); !strings.Contains(a.Text, "already down") {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Sel = 0
	m = key(m, "x")
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "no unload API") {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Rows[2].Health.Up = true
	m.Serve.Sel = 2
	m = key(m, "x")
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "not ours") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeLoadGuards(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 0
	m.Serve.Rows[0].Health.Kind = serve.KindOllama
	m.Cfg.Model.Name = ""
	if cmd := m.serveLoad(); cmd != nil {
		t.Error("blank model must not pull")
	} else if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Sel = 2
	if cmd := m.serveLoad(); cmd != nil {
		t.Error("sidecar row must not pull")
	} else if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "no load API") {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Rows[1].Health = serve.Health{Up: false, Kind: serve.KindOllama, Err: "down"}
	m.Serve.Sel = 1
	if cmd := m.serveLoad(); cmd != nil {
		t.Error("down row must not pull")
	} else if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "select an installed") {
		t.Errorf("alert=%+v", a)
	}
}

func TestServeLoadDone(t *testing.T) {
	m := serveModel(t)
	m.Serve.Loading = "qwen3:8b"
	um, _ := m.Update(serveLoadMsg{model: "qwen3:8b"})
	m = um.(Model)
	if m.Serve.Loading != "" {
		t.Error("loading must clear")
	}
	if a := lastAlert(m); !strings.Contains(a.Text, "resident action confirmed for qwen3:8b") {
		t.Errorf("alert=%+v", a)
	}
	um, _ = m.Update(serveLoadMsg{model: "qwen3:8b", err: errTest})
	m = um.(Model)
	if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("failed pull must err: %+v", a)
	}
}

func TestServeEnter(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m = key(m, "enter")
	if a := lastAlert(m); !strings.Contains(a.Text, "s start") {
		t.Errorf("alert=%+v", a)
	}
	m.Serve.Sel = 1
	m = key(m, "enter")
	if m.Cfg.Model.Server != serve.OllamaURL {
		t.Errorf("enter on up row must use it: %q", m.Cfg.Model.Server)
	}
}

func TestServeSelects(t *testing.T) {
	m := serveModel(t)
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "up")
	if m.Serve.Sel != 1 {
		t.Errorf("sel=%d, want 1", m.Serve.Sel)
	}
}

func TestServeEscChat(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/status")
	if !m.serveActive() {
		t.Fatal("not on status")
	}
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Errorf("view=%q, want chat", m.Tabs[m.Active].at.view)
	}
}

func TestServeKeepsSelection(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m.onServeProbed(serveProbedMsg{rows: m.Serve.Rows})
	if m.Serve.Sel != 2 {
		t.Errorf("sel=%d, want 2", m.Serve.Sel)
	}
	m.Serve.Sel = 9
	m.onServeProbed(serveProbedMsg{rows: m.Serve.Rows[:2]})
	if m.Serve.Sel != 0 {
		t.Errorf("stale sel=%d, want 0", m.Serve.Sel)
	}
}

func TestServeRender(t *testing.T) {
	m := serveModel(t)
	m.Width, m.Height = 160, 30
	got := viewportServePages(m, m.Width, 10)
	m.Width = 80
	narrow := viewportServePages(m, m.Width, 10)
	for _, want := range []string{"select", "enter", "use", "start", "unload", "load", "device", "model", "refresh", "esc", "chat"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow screen omits %q", want)
		}
	}
	for _, want := range []string{"RUNTIME", "DECISION SIDECAR", "worker", "ollama", "sidecar",
		"●", "○", "down", "qwen3:8b", "[worker]", "u use", "s start", "x unload", "l load", "d stage device", "m edit model", "esc chat"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen omits %q:\n%s", want, got)
		}
	}
}

func TestServeProbeEntry(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Fixed baseline excludes optional installed launchers.
	isolateModelRoots(t)
	m := New(testCfg(), "test")
	msg := m.probeServeWith(&http.Client{Transport: &pathStub{}})()
	probed, ok := msg.(serveProbedMsg)
	if !ok {
		t.Fatalf("msg=%T", msg)
	}
	if len(probed.rows) != 3 || !probed.rows[0].Worker || !probed.rows[2].Sidecar {
		t.Errorf("rows=%+v", probed.rows)
	}
}

func TestMemCalcArithmetic(t *testing.T) {
	big := hw.Profile{MemTotalBytes: 128 << 30, CPUs: 16}
	// 128k worker + 4 agents x 64k subs = 384k tokens x 2KB.
	tokens, bytes, warn := memCalc(128000, 4, 64000, big)
	if tokens != 384000 || bytes != 384000*2048 || warn {
		t.Errorf("tokens=%d bytes=%d warn=%v", tokens, bytes, warn)
	}
	// 384k tokens x 2KB ≈ 0.73GiB: over 512MiB warns.
	small := hw.Profile{MemTotalBytes: 512 << 20, CPUs: 4}
	if _, _, warn := memCalc(128000, 4, 64000, small); !warn {
		t.Error("want warn over hardware")
	}
	// Zeros inherit: sub window from worker, agents from hardware
	// (16 CPUs / 2 = 8, 128 GiB / 8 = 16 → 8).
	tokens, _, _ = memCalc(1000, 0, 0, big)
	if tokens != 1000+8*1000 {
		t.Errorf("tokens=%d", tokens)
	}
}

func TestMemCalcWarnNeedsHardware(t *testing.T) {
	if _, _, warn := memCalc(1<<30, 99, 1<<20, hw.Profile{}); warn {
		t.Error("unknown hardware must not warn")
	}
}

func TestMemoryReadoutUnknown(t *testing.T) {
	m := New(testCfg(), "test")
	m.Serve.MemErr = "no such device"
	if out := m.renderMemory(); !strings.Contains(out, "hardware unknown") {
		t.Errorf("out=%q", out)
	}
}
