package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"strings"
	"testing"
)

func formModel(t *testing.T) Model {
	t.Helper()
	m := setupFixture(t, 80, 24)
	m.openConnectionForm()
	return m
}
func TestConnectionFormOwnsKeysAndStagesWithoutEffects(t *testing.T) {
	m := formModel(t)
	f := m.Setup.Add
	f.Values[3].SetValue("coder")
	before := m.Cfg
	tabs := len(m.Tabs)
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = um.(Model)
	if cmd == nil || len(m.Tabs) != tabs || m.Setup.Add != f {
		t.Fatal("catalog test key opened a tab or lost form")
	}
	m = viewportKey(m, tea.KeyTab)
	if f.Field != 1 {
		t.Fatal("form did not own Tab")
	}
	m = viewportKey(m, tea.KeyCtrlS)
	if m.Setup.Stage != 1 || m.Setup.Model.Name != "coder" || m.Setup.Add != nil {
		t.Fatalf("staging failed: %+v", m.Setup)
	}
	if !reflect.DeepEqual(before, m.Cfg) {
		t.Fatal("staging modified live config")
	}
	if _, err := os.Stat(m.CfgPath); !os.IsNotExist(err) {
		t.Fatal("staging wrote project")
	}
}
func TestConnectionFolderKeepsWireAliasAndValidatesShards(t *testing.T) {
	m := formModel(t)
	dir := t.TempDir()
	for name, value := range map[string]string{"config.json": "{\"model_type\":\"qwen\"}", "tokenizer.json": "{}", "model.safetensors.index.json": "{\"weight_map\":{\"a\":\"one.safetensors\",\"b\":\"two.safetensors\"}}", "one.safetensors": "weights"} {
		os.WriteFile(filepath.Join(dir, name), []byte(value), 0600)
	}
	f := m.Setup.Add
	f.Values[3].SetValue("served-alias")
	f.Values[4].SetValue(dir)
	if _, err := f.candidate(m.Cfg); err == nil || !strings.Contains(err.Error(), "two.safetensors") {
		t.Fatalf("missing shard not explained: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "two.safetensors"), []byte("weights"), 0600)
	c, err := f.candidate(m.Cfg)
	if err != nil || c.Model.Name != "served-alias" {
		t.Fatalf("wire alias replaced: %+v %v", c.Model, err)
	}
	f.Values[3].SetValue("")
	c, err = f.candidate(m.Cfg)
	canonical, _ := filepath.EvalSymlinks(dir)
	if err != nil || c.Model.Name != canonical {
		t.Fatalf("folder default missing: %+v %v", c.Model, err)
	}
}
func TestSetupStatusSelectionStaysGlobalWithoutProjectCheckLeak(t *testing.T) {
	m := setupFixture(t, 80, 24)
	for i := 0; i < 3; i++ {
		m = viewportKey(m, tea.KeyEnter)
	}
	m.setupSave()
	if m.Setup.Err != "" {
		t.Fatal(m.Setup.Err)
	}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Kind: serve.KindOpenAI, URL: "http://127.0.0.1:9999/v1", Model: "changed"}, Health: serve.Health{Up: true, Runtime: "openai"}, Have: true, IsModel: true}}
	m.serveUse()
	second, _, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || second.Model.Name != "changed" {
		t.Fatalf("Status change not reusable: %+v %v", second.Model, err)
	}
	raw, _ := os.ReadFile(m.CfgPath)
	if strings.Contains(string(raw), "model:") {
		t.Fatal("Status shadowed global choices")
	}
}
func TestCustomSafetensorsRecipeGetsLoadableAsset(t *testing.T) {
	isolateModelRoots(t)
	m := settingsModel(t)
	dir := t.TempDir()
	for name, value := range map[string]string{"config.json": "{}", "tokenizer.json": "{}", "model.safetensors": "weights"} {
		os.WriteFile(filepath.Join(dir, name), []byte(value), 0600)
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{\"model_type\":\"fixture\"}"), 0600)
	m.Cfg.Serve.Models = []config.SavedModel{{Server: "http://127.0.0.1:19998/v1", Name: dir, Path: dir}}
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom", Kind: "openai", Runtime: "openai", URL: "http://127.0.0.1:19998/v1", Argv: []string{"custom", "--model", "{model}"}}}
	msg := m.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg)
	found := false
	for _, r := range msg.rows {
		if r.Asset != nil && r.Ep.Label == "custom" {
			found = true
			if !r.Asset.LoadCandidate() {
				t.Fatal(r.Asset)
			}
		}
	}
	if !found {
		t.Fatal("custom safetensors omitted")
	}
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist }

func TestChangingJudgeKeepsOldOwnedSidecarVisible(t *testing.T) {
	m := settingsModel(t)
	p, err := serve.Start(serve.Recipe{Name: "old-judge-fixture", Argv: []string{"sleep", "30"}}, filepath.Join(t.TempDir(), "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	old := m.Cfg.Decision.Server
	m.Serve.Spawned = map[string]*serve.Proc{old: p}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Kind: serve.KindSidecar, URL: old}, Sidecar: true}}
	next := m.Cfg.Decision
	next.Runtime = "openai"
	next.Server = "http://127.0.0.1:19997/v1"
	next.Model = "judge"
	m.Settings.DecisionDraft = &next
	if cmd := m.applyDecisionSelection(); cmd != nil {
		t.Fatal("chat selection attempted native activation")
	}
	for i := 0; i < 2; i++ {
		found := false
		for _, e := range m.serveEndpoints() {
			if e.URL == old {
				found = true
				if e.Kind != serve.KindSidecar {
					t.Fatal("old native identity lost")
				}
			}
		}
		if !found {
			t.Fatal("owned child disappeared")
		}
		msg := m.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg)
		m.onServeProbed(msg)
	}
	if p.PID() == 0 {
		t.Fatal("selection stopped old process without user command")
	}
	for i, r := range m.Serve.Rows {
		if r.Ep.URL == old && r.Sidecar {
			m.Serve.Sel = i
			break
		}
	}
	m.serveUnload()
	if p.PID() != 0 {
		t.Fatal("unhealthy old owned sidecar could not be stopped")
	}
}

func TestCustomNativeJudgeRecipeWinsAndExplicitPresetResolves(t *testing.T) {
	t.Chdir("../..")
	m := settingsModel(t)
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom-d1", Kind: serve.KindSidecar, URL: m.Cfg.Decision.Server, Argv: []string{"custom-judge", "--model", "{model}"}}}
	row := ServeRow{Ep: serve.Endpoint{Kind: serve.KindSidecar, URL: m.Cfg.Decision.Server}, Sidecar: true}
	rec := m.recipeFor(&row)
	if rec == nil || rec.Argv[0] != "custom-judge" {
		t.Fatal("explicit native argv ignored", rec)
	}
	m.Cfg.Serve.Recipes[0].Argv = nil
	if rec := m.recipeFor(&row); rec == nil || len(rec.Argv) != 0 {
		t.Fatal("blank launcher must remain external", rec)
	}
	m.Cfg.Serve.Recipes[0].Launcher = "preset"
	for _, rec := range m.serveRecipes() {
		if rec.Name == "custom-d1" {
			if len(rec.Argv) == 0 {
				t.Fatal("known native launcher absent")
			}
			return
		}
	}
	t.Fatal("native recipe missing")
}

func TestServeCandidatesStayUnderTheirServerAndRecipe(t *testing.T) {
	a := &serve.Recipe{Name: "mlx", URL: "http://localhost:8080/v1"}
	b := &serve.Recipe{Name: "local-mlx", URL: "http://localhost:19092/v1"}
	c := &serve.Recipe{Name: "alternative", URL: b.URL}
	server := func(r *serve.Recipe) ServeRow {
		return ServeRow{Ep: serve.Endpoint{URL: r.URL, Label: r.Name}, Recipe: r}
	}
	model := func(r *serve.Recipe) ServeRow {
		row := server(r)
		row.IsModel = true
		row.Ep.Model = "same-checkpoint"
		return row
	}
	rows := groupServeRows([]ServeRow{server(a), server(b), server(c), model(a), model(b), model(c)})
	if len(rows) != 6 {
		t.Fatal("lost candidates", rows)
	}
	for i, r := range []*serve.Recipe{a, b, c} {
		if rows[2*i].IsModel || !rows[2*i+1].IsModel || rows[2*i].Recipe.Name != r.Name || rows[2*i+1].Recipe.Name != r.Name || rows[2*i+1].Ep.URL != r.URL {
			t.Fatalf("candidate grouped under wrong server: %+v", rows)
		}
	}
}
