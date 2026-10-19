package tui

import (
	"context"
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/serve"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestExplicitManageFreshNoDiscoveryOrGuessedEndpoints(t *testing.T) {
	m := New(config.Defaults(), "test")
	if endpoints := m.serveEndpoints(); len(endpoints) != 0 {
		t.Fatalf("fresh guessed endpoints: %+v", endpoints)
	}
	scanned := false
	result := m.probeServeWithDiscovery(&http.Client{Transport: offlineTransport{}}, func(context.Context, *http.Client) ([]serve.DiscoveredServer, error) { scanned = true; return nil, nil })().(serveProbedMsg)
	if scanned || len(result.rows) != 0 {
		t.Fatalf("ambient discovery called=%t rows=%+v", scanned, result.rows)
	}
}
func TestExplicitManageOfflineInteractionAndMachineRoundtrip(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.CfgPath = ""
	m = submit(m, "/manage")
	m.Serve.Rows = nil
	m = key(m, "n")
	f := m.Setup.Add
	if f == nil || f.Mode != "server" {
		t.Fatal("Add server unavailable")
	}
	f.Values[0].SetValue("Offline dev")
	f.Values[2].SetValue("http://127.0.0.1:19999/v1")
	m = viewportKey(m, tea.KeyCtrlS)
	if m.Setup.Add != nil || len(m.Cfg.Serve.Recipes) != 1 {
		t.Fatalf("save failed: %+v", m.Setup.Add)
	}
	msg := m.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg)
	m.onServeProbed(msg)
	if len(m.Serve.Rows) != 1 || m.Serve.Rows[0].Ep.Label != "Offline dev" {
		t.Fatalf("offline server omitted: %+v", m.Serve.Rows)
	}
	m = key(m, "+")
	if m.Setup.Add == nil || !reflect.DeepEqual(m.Setup.Add.visibleFields(), []int{3, 4}) {
		t.Fatal("Add model fields")
	}
	m.Setup.Add.Values[3].SetValue("model-one")
	m = viewportKey(m, tea.KeyCtrlS)
	m = key(m, "+")
	m.Setup.Add.Values[3].SetValue("model-two")
	m = viewportKey(m, tea.KeyCtrlS)
	reopened, _, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Serve.Models) != 2 || reopened.Serve.Models[0].Name != "model-one" || reopened.Serve.Models[1].Name != "model-two" {
		t.Fatalf("models lost: %+v", reopened.Serve.Models)
	}
	next := New(reopened, "test")
	next.onServeProbed(next.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg))
	if len(next.Serve.Rows) != 3 {
		t.Fatalf("offline saved models missing: %+v", next.Serve.Rows)
	}
	next.Serve.Sel = 1
	next.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	next.serveUse()
	if next.Cfg.Model.Name != "model-one" {
		t.Fatal("offline role assignment blocked")
	}
}
func TestExplicitManageStatusReadOnlyAndAdvancedHidden(t *testing.T) {
	m := serveModelWithModels(t)
	m = submit(m, "/status")
	before := m.Cfg
	m = key(m, "s")
	m = key(m, "x")
	m = key(m, "n")
	if m.serveActive() || m.Setup.Add != nil || !reflect.DeepEqual(before, m.Cfg) || len(m.Serve.Spawned) != 0 {
		t.Fatal("Status admitted management")
	}
	m.Input.Reset()
	m = submit(m, "/manage")
	m = key(m, "n")
	view := m.renderConnection(120, 30)
	if strings.Contains(view, "JSON") || strings.Contains(view, "Launch directory") {
		t.Fatal("advanced fields shown ordinarily")
	}
	m = viewportKey(m, tea.KeyF2)
	view = m.renderConnection(120, 30)
	if !strings.Contains(view, "JSON") {
		t.Fatal("Advanced toggle missing")
	}
}

func TestExplicitManageComposerDeletionDoesNotRemoveConnection(t *testing.T) {
	for _, keyType := range []tea.KeyType{tea.KeyBackspace, tea.KeyDelete} {
		for _, draft := range []string{"draft text", "/status ", " "} {
			t.Run(fmt.Sprintf("%d/%q", keyType, draft), func(t *testing.T) {
				m := setupFixture(t, 100, 30)
				m.Setup = SetupState{}
				m.navigate(ViewManage, "")
				endpoint := "http://127.0.0.1:19995/v1"
				m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "saved", Kind: "openai", URL: endpoint}}
				m.Cfg.Serve.Models = []config.SavedModel{{Server: endpoint, Name: "saved-model"}}
				m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{URL: endpoint, Model: "saved-model"}, IsModel: true}}
				if err := m.saveManaged(m.Cfg); err != nil {
					t.Fatal(err)
				}
				path, _ := config.ConnectionsPath()
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				m.Input.SetValue(draft)
				m.Input.SetCursor(len(draft))
				m = viewportKey(m, keyType)
				after, err := os.ReadFile(path)
				if err != nil || string(before) != string(after) || len(m.Cfg.Serve.Models) != 1 {
					t.Fatalf("composer deletion changed saved connection: %v", err)
				}
			})
		}
	}
}

func TestExplicitManageEditsServerAndRenamesModelOnce(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.navigate(ViewManage, "")
	ep := "http://127.0.0.1:19994/v1"
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "before", Kind: "openai", Runtime: "openai", URL: ep}}
	m.Cfg.Serve.Models = []config.SavedModel{{Server: ep, Name: "model-before"}}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "before", URL: ep}, Recipe: &serve.Recipe{Runtime: "openai"}}}
	m.openManagedForm("edit")
	m.Setup.Add.Values[0].SetValue("after")
	m = viewportKey(m, tea.KeyCtrlS)
	if m.Setup.Add != nil {
		t.Fatalf("server-only edit refused: %s", m.Setup.Add.Err)
	}
	if len(m.Cfg.Serve.Recipes) != 1 || m.Cfg.Serve.Recipes[0].Name != "after" {
		t.Fatalf("server edit lost identity: %+v", m.Cfg.Serve.Recipes)
	}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "after", URL: ep, Model: "model-before"}, IsModel: true, Recipe: &serve.Recipe{Runtime: "openai"}}}
	m.openManagedForm("edit")
	m.Setup.Add.Values[3].SetValue("model-after")
	m = viewportKey(m, tea.KeyCtrlS)
	if m.Setup.Add != nil {
		t.Fatalf("model edit refused: %s", m.Setup.Add.Err)
	}
	if len(m.Cfg.Serve.Models) != 1 || m.Cfg.Serve.Models[0].Name != "model-after" {
		t.Fatalf("model rename duplicated or retained old identity: %+v", m.Cfg.Serve.Models)
	}
}

func TestExplicitManageServerNameCollisionPreservesConnections(t *testing.T) {
	for _, mode := range []string{"server", "edit"} {
		t.Run(mode, func(t *testing.T) {
			m := setupFixture(t, 100, 30)
			m.Setup = SetupState{}
			m.navigate(ViewManage, "")
			ep := "http://127.0.0.1:19993/v1"
			m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "first", Kind: "openai", URL: ep}, {Name: "second", Kind: "openai", URL: "http://127.0.0.1:19992/v1"}}
			before := append([]config.RecipeConfig(nil), m.Cfg.Serve.Recipes...)
			m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "first", URL: ep}, Recipe: &serve.Recipe{}}}
			m.openManagedForm(mode)
			m.Setup.Add.Values[0].SetValue("second")
			m = viewportKey(m, tea.KeyCtrlS)
			if m.Setup.Add == nil || !strings.Contains(m.Setup.Add.Err, "already exists") || !reflect.DeepEqual(before, m.Cfg.Serve.Recipes) {
				t.Fatalf("collision changed existing connections: %+v %+v", m.Setup.Add, m.Cfg.Serve.Recipes)
			}
		})
	}
}

func TestExplicitManageLegacyEmptyRuntimeCanBeEdited(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.navigate(ViewManage, "")
	ep := "http://127.0.0.1:19991/v1"
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "legacy", Kind: "openai", URL: ep}}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "legacy", Kind: "openai", URL: ep}, Recipe: &serve.Recipe{}}}
	m.openManagedForm("edit")
	m = viewportKey(m, tea.KeyCtrlS)
	if m.Setup.Add != nil {
		t.Fatalf("legacy edit failed: %s", m.Setup.Add.Err)
	}
}

func TestExplicitManageSelectedRecipeOverridesOldRuntimeAtSameURL(t *testing.T) {
	for _, reported := range []string{"", "openai"} {
		t.Run("reported="+reported, func(t *testing.T) {
			m := setupFixture(t, 100, 30)
			m.Setup = SetupState{}
			m.Cfg.Model.Runtime = "openai"
			m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{URL: m.Cfg.Model.Server, Model: "explicit-mlx"}, IsModel: true, Recipe: &serve.Recipe{Runtime: "mlx"}, Health: serve.Health{Runtime: reported}}}
			m.serveUse()
			want := "mlx"
			if reported != "" {
				want = reported
			}
			if m.Cfg.Model.Runtime != want || m.Cfg.Model.Name != "explicit-mlx" {
				t.Fatalf("wrong runtime priority: %+v", m.Cfg.Model)
			}
		})
	}
}

func TestExplicitManageNativeLauncherIsExplicit(t *testing.T) {
	repoRoot(t)
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.navigate(ViewManage, "")
	m.openManagedForm("server")
	f := m.Setup.Add
	f.Values[0].SetValue("native")
	f.Values[1].SetValue("system_one")
	f.Values[2].SetValue("http://127.0.0.1:19990")
	c, err := f.candidate(m.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.Cfg = c
	assertRecipe := func(wantPreset bool) {
		t.Helper()
		for _, rec := range m.serveRecipes() {
			if rec.Name != "native" {
				continue
			}
			if (len(rec.Argv) > 0) != wantPreset {
				t.Fatalf("launcher external/preset mismatch: %+v", rec)
			}
			if wantPreset && (rec.Runtime != "system_one" || !strings.Contains(strings.Join(rec.Argv, " "), "--runtime system_one")) {
				t.Fatalf("native preset lost contract: %+v", rec)
			}
			return
		}
		t.Fatal("saved native recipe missing")
	}
	assertRecipe(false)
	f.Mode, f.OriginalName = "edit", "native"
	f.Values[7].SetValue("preset")
	c, err = f.candidate(m.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.Cfg = c
	assertRecipe(true)
}

func TestExplicitManagePresetCyclingPreservesCustomExecutable(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup = SetupState{}
	m.navigate(ViewManage, "")
	m.openManagedForm("server")
	f := m.Setup.Add
	f.Field = 1
	f.Values[1].SetValue("ollama")
	f.Values[7].SetValue("preset")
	m = viewportKey(m, tea.KeyRight)
	if f.Values[1].Value() != "llama.cpp" || f.Values[7].Value() != "" {
		t.Fatal("unsupported preset retained after runtime cycle")
	}
	f.Values[1].SetValue("custom")
	f.Values[7].SetValue("my-explicit-launcher")
	m = viewportKey(m, tea.KeyRight)
	if f.Values[7].Value() != "my-explicit-launcher" {
		t.Fatal("cycle overwrote explicit executable")
	}
}
func TestExplicitManageRuntimeMetricsReportedUnknownAndStaleRead(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Seq = 5
	raw, _ := json.Marshal(llm.RequestMeasurement{Requested: "selected", Served: "actual", Seconds: 2, Usage: llm.Usage{Out: 20, Reported: true}})
	m.onEvent(eventMsg{seq: 5, kind: "model_request", text: string(raw)})
	m.onEvent(eventMsg{seq: 5, kind: "tool", text: "Read file"})
	raw, _ = json.Marshal(llm.RequestMeasurement{Requested: "selected", Failed: true})
	m.onEvent(eventMsg{seq: 5, kind: "model_request", text: string(raw)})
	if s := m.Chat.Runtime; s.Attempts != 2 || s.Generated != 20 || s.Failed != 1 || s.Tools != 1 || len(s.Models) != 1 || s.Models[0] != "actual" {
		t.Fatalf("metrics %+v", s)
	}
	view := m.renderRuntimeStatus(160, 35)
	if !strings.Contains(view, "partial") || !strings.Contains(view, "unknown") || strings.Contains(view, "selected,") {
		t.Fatal(view)
	}
	before := m.Chat.Runtime
	m = viewportUpdate(m, runtimeMetricsMsg{Owner: m.origin(), Seq: 4, Metrics: runtimeMetrics{}})
	if !reflect.DeepEqual(before, m.Chat.Runtime) {
		t.Fatal("stale snapshot erased live metrics")
	}
}
func TestExplicitManageRuntimeRestoresOnlyNamedSessionRuns(t *testing.T) {
	m := New(testCfg(), "test")
	m.SessionsPath = t.TempDir()
	m.Chat.SessionIDs = []string{"one", "two"}
	for _, id := range m.Chat.SessionIDs {
		raw, _ := json.Marshal(llm.RequestMeasurement{Served: id, Usage: llm.Usage{Out: 7, Reported: true}, Seconds: 1})
		if err := session.Append(m.SessionsPath, id, session.Event{Kind: "model_request", Text: string(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(m.SessionsPath, "unrelated.jsonl"), []byte("corrupt unrelated history"), 0600)
	msg := m.fetchRuntimeMetrics()().(runtimeMetricsMsg)
	m = viewportUpdate(m, msg)
	if m.Chat.Runtime.Attempts != 2 || m.Chat.Runtime.Generated != 14 {
		t.Fatalf("restored %+v", m.Chat.Runtime)
	}
}

func TestExplicitManageRetainedSetupAcceptsOwnSaves(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Setup.Check.SetValue("go test ./...")
	m.Serve.Rows = nil
	m = submit(m, "/manage")
	m = key(m, "n")
	m.Setup.Add.Values[0].SetValue("Saved offline")
	m.Setup.Add.Values[2].SetValue("http://127.0.0.1:19998/v1")
	m = viewportKey(m, tea.KeyCtrlS)
	m.onServeProbed(m.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg))
	m = key(m, "+")
	m.Setup.Add.Values[3].SetValue("saved-coder")
	m = viewportKey(m, tea.KeyCtrlS)
	m.onServeProbed(m.probeServeWith(&http.Client{Transport: offlineTransport{}})().(serveProbedMsg))
	for i, row := range m.Serve.Rows {
		if row.IsModel && row.Ep.Model == "saved-coder" {
			m.Serve.Sel = i
			break
		}
	}
	m = key(m, "u")
	if m.Cfg.Model.Name != "saved-coder" {
		t.Fatal("Manage Coding selection failed")
	}
	m.OpenSetup()
	for i := 0; i < 4; i++ {
		m = viewportKey(m, tea.KeyEnter)
	}
	if m.Setup.Err != "" || m.Cfg.Model.Name != "saved-coder" || m.Cfg.Coding.CheckCommand != "go test ./..." {
		t.Fatalf("retained Setup failed: %s %+v", m.Setup.Err, m.Cfg.Model)
	}
}
func TestExplicitManageNativeSelectionUsesItsServerAndContract(t *testing.T) {
	m := settingsModel(t)
	m.Cfg.Decision.Disabled = true
	ep := "http://127.0.0.1:19997"
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Kind: serve.KindSidecar, URL: ep, Model: "native-ID"}, IsModel: true, Sidecar: true, Recipe: &serve.Recipe{Runtime: "system_one"}}}
	m.serveUseJudge()
	draft := m.Settings.DecisionDraft
	if draft == nil || draft.Disabled || draft.Server != ep || draft.Runtime != "system_one" || draft.Model != "native-ID" {
		t.Fatalf("wrong native selection: %+v", draft)
	}
	if cmd := m.applyDecisionSelection(); cmd != nil || m.Cfg.Decision.Disabled || m.Cfg.Decision.Server != ep {
		t.Fatal("unowned target was activated or wrong target committed")
	}
}
func TestExplicitManageCanonicalAliasRemoveClearsBothRoles(t *testing.T) {
	m := setupFixture(t, 100, 30)
	path := filepath.Join(t.TempDir(), "checkpoint")
	ep := "http://127.0.0.1:19996/v1"
	m.Cfg.Model.Server = ep
	m.Cfg.Model.Name = path
	m.Cfg.Model.Runtime = "mlx"
	m.Cfg.Decision.Server = ep
	m.Cfg.Decision.Model = path
	m.Cfg.Decision.Runtime = "mlx"
	m.Cfg.Serve.Models = []config.SavedModel{{Server: ep, Name: "friendly-ID", Path: path}}
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{URL: ep, Model: "friendly-ID"}, IsModel: true, Asset: &serve.Asset{Path: path}}}
	m.removeManaged()
	if m.Cfg.Model.Name != "" || !m.Cfg.Decision.Disabled || len(m.Cfg.Serve.Models) != 0 {
		t.Fatalf("removed alias resurrected role: %+v %+v", m.Cfg.Model, m.Cfg.Decision)
	}
}
