package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestD09AvailableEnterReturnsCommand(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 3
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if um.(Model).Serve.Loading == "" {
		t.Fatal("returned model lost pending action")
	}
	if cmd == nil {
		t.Fatal("available-model Enter discarded action command")
	}
}
func TestD09RowKindIdentity(t *testing.T) {
	server := ServeRow{Ep: serve.Endpoint{URL: "http://same/v1", Model: "same"}}
	model := server
	model.IsModel = true
	if serveKeyOf(server) == serveKeyOf(model) {
		t.Fatal("server and model keys collide")
	}
}
func TestD09ModelsRoutesStatus(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/models /local/path with  spaces")
	if !m.serveActive() {
		t.Fatal("models remains a phase stub")
	}
}
func TestD09CustomRecipeEndpoint(t *testing.T) {
	m := New(testCfg(), "test")
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom", Kind: serve.KindOpenAI, URL: "http://sentinel.invalid/custom/v1"}}
	for _, e := range m.serveEndpoints() {
		if e.URL == m.Cfg.Serve.Recipes[0].URL {
			return
		}
	}
	t.Fatal("custom recipe endpoint absent")
}

func isolateModelRoots(t *testing.T) {
	t.Helper()
	t.Setenv("HF_HUB_CACHE", t.TempDir())
	t.Setenv("OLLAMA_MODELS", filepath.Join(t.TempDir(), "absent"))
}
func boundModelFixture(t *testing.T) (Model, func() string) {
	t.Helper()
	isolateModelRoots(t)
	log := fakeOllama(t)
	var mu sync.Mutex
	resident := map[string]bool{"qwen3:8b": true, "other": true}
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"qwen3:8b"},{"name":"llama3"},{"name":"other"}]}`))
		case "/api/ps":
			var models []map[string]string
			for name, yes := range resident {
				if yes {
					models = append(models, map[string]string{"name": name})
				}
			}
			if models == nil {
				models = []map[string]string{}
			}
			json.NewEncoder(w).Encode(map[string]any{"models": models})
		case "/api/generate":
			var body struct {
				Model     string
				KeepAlive *int `json:"keep_alive"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			load := body.KeepAlive == nil
			resident[body.Model] = load
			calls = append(calls, fmt.Sprintf("%s:%t", body.Model, load))
			json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "done": true})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OLLAMA_HOST", "http://unselected-sentinel.invalid")
	m := serveModelWithModels(t)
	for i := range m.Serve.Rows {
		if m.Serve.Rows[i].Ep.Kind == serve.KindOllama {
			m.Serve.Rows[i].Ep.URL = srv.URL + "/v1"
		}
	}
	return m, func() string {
		mu.Lock()
		defer mu.Unlock()
		if !resident["other"] {
			t.Error("unselected model released")
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Error("unbound Ollama CLI invoked")
		}
		return strings.Join(calls, ",")
	}
}
func executeServeKey(t *testing.T, m Model, keyName string) Model {
	t.Helper()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyName)}
	if keyName == "enter" {
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	}
	um, cmd := m.Update(msg)
	m = um.(Model)
	if cmd == nil {
		t.Fatal("resident action Cmd missing")
	}
	if m.Serve.Loading == "" {
		t.Fatal("returned model lost unsettled action")
	}
	result := cmd()
	um, _ = m.Update(result)
	m = um.(Model)
	if lastAlert(m).Kind == "err" {
		t.Fatal(lastAlert(m))
	}
	return m
}
func TestD09ModelSelectionUsesExactAPIBaseAndDoesNotLoad(t *testing.T) {
	m, calls := boundModelFixture(t)
	m.Serve.Sel = 2
	m.Serve.Rows[2].Health.APIBase = "http://exact.invalid/prefix"
	m = key(m, "u")
	if m.Cfg.Model.Server != "http://exact.invalid/prefix" || calls() != "" {
		t.Fatal("selection loaded or lost base")
	}
}
func TestD09ExactFocusAndKindStableAcrossRefresh(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/models /local/path with  spaces  ")
	want := "/local/path with  spaces  "
	if m.Serve.Focus != want {
		t.Fatalf("raw focus %q", m.Serve.Focus)
	}
	m.onServeProbed(serveProbedMsg{rows: []ServeRow{{Ep: serve.Endpoint{URL: "same", Model: want}}, {Ep: serve.Endpoint{URL: "same", Model: want}, IsModel: true}}})
	if m.Serve.Sel != 1 {
		t.Fatal("exact model focus lost")
	}
	m.onServeProbed(serveProbedMsg{rows: m.Serve.Rows})
	if m.Serve.Sel != 1 {
		t.Fatal("model/server key collision changed focus")
	}
}
func TestD09ServingActionCapturesRowAndQuitWaitsCancellation(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 3
	m.Chat.Running = true
	if m.serveLoad() != nil {
		t.Fatal("active worker memory operation admitted")
	}
	m.Chat.Running = false
	m.Wf.Busy = "approach"
	if m.serveUnload() != nil {
		t.Fatal("workflow memory operation admitted")
	}
	m.Wf.Busy = ""
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()
	m.Serve.Rows[3].Ep.URL = srv.URL + "/v1"
	cmd := m.serveLoad()
	if cmd == nil {
		t.Fatal("action missing")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-entered
	m.Serve.Sel = 2
	if m.requestQuit() != nil || !m.quitPending || m.Serve.Loading == "" {
		close(release)
		t.Fatal("quit did not wait/cancel")
	}
	select {
	case msg := <-done:
		um, next := m.Update(msg)
		m = um.(Model)
		close(release)
		if next == nil || next() != tea.Quit() {
			t.Fatal("settled quit missing")
		}
		if m.Serve.Loading != "" || lastAlert(m).Kind != "err" {
			t.Fatal("cancelled action mislabeled")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("serving cancellation did not settle")
	}
}

func TestD09HeldServingActionRefusesActualChatAdmission(t *testing.T) {
	var modelCalls atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"component reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer worker.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write([]byte(`{"models":[]}`))
	}))
	defer native.Close()
	m := serveModelWithModels(t)
	m.Serve.Sel = 3
	m.Serve.Rows[3].Ep.URL = native.URL + "/v1"
	action := m.serveLoad()
	done := make(chan tea.Msg, 1)
	go func() { done <- action() }()
	<-entered
	m.Cfg.Model.Name = "component-model"
	m.Cfg.Model.Server = worker.URL + "/v1"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Decision.Server = worker.URL
	m.ModelName = "component-model"
	m.Server = m.Cfg.Model.Server
	m.StorePath = filepath.Join(t.TempDir(), "untouched.db")
	m.SessionsPath = t.TempDir()
	m.navigate(ViewChat, "")
	m.Input.SetValue("ordinary coding request")
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	admitted := m.Chat.Running
	if m.Chat.cancel != nil {
		m.Chat.cancel()
	}
	if cmd := m.compactChat(); cmd != nil {
		t.Error("manual compact admitted while serving held")
	}
	if cmd := m.wfLaunch(WfRow{Approach: "held-control"}, false); cmd != nil {
		t.Error("workflow admitted while serving held")
		if m.Wf.cancel != nil {
			m.Wf.cancel()
		}
	}
	m.Serve.cancel()
	<-done
	close(release)
	if admitted || modelCalls.Load() != 0 {
		t.Fatalf("held serving allowed admission=%t actual model HTTPcalls=%d", admitted, modelCalls.Load())
	}
	if _, err := os.Stat(m.StorePath); !os.IsNotExist(err) {
		t.Fatal("blocked chat created store before serving settled")
	}
}
func TestD09DiskCandidatesAndExactNameFocus(t *testing.T) {
	isolateModelRoots(t)
	root := t.TempDir()
	path := filepath.Join(root, "candidate with  spaces")
	os.Mkdir(path, 0700)
	for name, body := range map[string]string{"config.json": `{"model_type":"component-only"}`, "tokenizer.json": `{}`, "model.safetensors": "metadata stat fixture only"} {
		os.WriteFile(filepath.Join(path, name), []byte(body), 0600)
	}
	m := New(testCfg(), "test")
	m.Cfg.Serve.Models = []config.SavedModel{{Server: m.Cfg.Model.Server, Name: path, Path: path}}
	// Explicit metadata-only recipe: this test selects a checkpoint, never
	// launches MLX or depends on a platform's installed preset.
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "my-mlx", Kind: "openai", Runtime: "mlx", URL: m.Cfg.Model.Server, Argv: []string{"fixture-model-server", "--model", "{model}"}}}
	msg := m.probeServeWith(&http.Client{Transport: &pathStub{}})().(serveProbedMsg)
	var found ServeRow
	for _, r := range msg.rows {
		if r.Asset != nil && r.Asset.ID == filepath.Base(path) {
			found = r
		}
	}
	if found.Asset == nil || !found.Asset.Complete || found.Resident || found.Ours {
		t.Fatalf("candidate falsely ready: %+v", found)
	}
	m.Serve.Focus = filepath.Base(path)
	m.onServeProbed(msg)
	if m.serveCur().Asset == nil || m.serveCur().Asset.Path != found.Asset.Path {
		t.Fatal("exact disk name not focused")
	}
	m.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	m.serveUse()
	if m.Cfg.Model.Name != found.Asset.Path || m.Cfg.Model.Runtime != "mlx" || len(m.Serve.Spawned) != 0 {
		t.Fatal("candidate select changed memory/service instead of next selection")
	}
	if !strings.Contains(m.renderServe(160, 20), "compatibility unconfirmed") {
		t.Fatal("candidate lost compatibility caveat")
	}
}
