package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/serve"
	"strings"
	"testing"
)

func TestGGUFCandidateListedForEachConfiguredServer(t *testing.T) {
	isolateModelRoots(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUFfixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := settingsModel(t)
	m.Cfg.Serve.ModelDirs = []string{dir}
	for _, name := range []string{"first", "second"} {
		m.Cfg.Serve.Models = append(m.Cfg.Serve.Models, config.SavedModel{Server: "http://127.0.0.1/" + name, Name: filepath.Join(dir, "model.gguf"), Path: filepath.Join(dir, "model.gguf")})
		m.Cfg.Serve.Recipes = append(m.Cfg.Serve.Recipes, config.RecipeConfig{Name: name, Kind: "openai", Runtime: "openai", URL: "http://127.0.0.1/" + name, Argv: []string{"server", "--model", "{model}"}})
	}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	seen := map[string]bool{}
	for _, row := range got.rows {
		if row.Asset != nil && row.Asset.Format == "gguf" {
			seen[row.Ep.Label] = true
			if row.Resident || row.Asset.Complete {
				t.Fatal("GGUF candidate called complete/resident")
			}
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("missing configured choice %v", seen)
	}
}

func TestGenericCatalogEnterSelectsWorkerWithoutLifecycle(t *testing.T) {
	m := settingsModel(t)
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Kind: serve.KindOpenAI, Label: "custom", URL: "http://127.0.0.1:8888/v1", Model: "catalog-model"}, Health: serve.Health{Kind: serve.KindOpenAI, Up: true, APIBase: "http://127.0.0.1:8888/v1"}, Have: true, IsModel: true}}
	if got := m.serveEnterLabel(); got != "use worker" {
		t.Errorf("label %q", got)
	}
	if cmd := m.serveEnter(); cmd != nil {
		t.Fatal("catalog selection started lifecycle command")
	}
	if len(m.Serve.Spawned) != 0 {
		t.Fatal("catalog selection created owned process")
	}
	if m.Cfg.Model.Name != "catalog-model" || m.Cfg.Model.Server != "http://127.0.0.1:8888/v1" {
		t.Fatalf("worker unchanged: %+v", m.Cfg.Model)
	}
	saved, _, err := config.Load(m.CfgPath)
	if err != nil || saved.Model.Name != "catalog-model" {
		t.Fatalf("selection not persisted: %+v %v", saved.Model, err)
	}
}

func TestNativeJudgeCatalogStagesExactLocalIdentity(t *testing.T) {
	for _, runtime := range []string{"laya", "system_one"} {
		t.Run(runtime, func(t *testing.T) {
			isolateModelRoots(t)
			path := filepath.Join(t.TempDir(), "local candidate")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"config.json": `{"model_type":"unknown"}`, "tokenizer.json": `{}`, "model.safetensors": "metadata only"} {
				if err := os.WriteFile(filepath.Join(path, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path, _ = filepath.EvalSymlinks(path)
			m := settingsModel(t)
			m.Cfg.Decision.Runtime, m.Cfg.Decision.Path = runtime, path
			m.Cfg.Decision.Revision, m.Cfg.Decision.TrustRemoteCode = "old-remote-revision", true
			before, worker := m.Cfg.Decision, m.Cfg.Model
			probe := m.probeServeWith(probeClient())
			// Inventory must use its captured configuration, not later UI edits.
			m.Cfg.Decision.Runtime = "system_one"
			got := probe().(serveProbedMsg)
			m.Cfg.Decision = before
			var rows []ServeRow
			for _, row := range got.rows {
				if row.Sidecar && row.Asset != nil {
					rows = append(rows, row)
				}
			}
			if len(rows) != 1 {
				t.Fatalf("native candidates = %d", len(rows))
			}
			row := rows[0]
			if row.Resident || row.Ours || row.Asset.Runtime != runtime || row.Ep.Model != before.Model {
				t.Fatalf("metadata became readiness: %+v", row)
			}
			m.Serve.Rows, m.Serve.Sel = rows, 0
			if label := m.serveEnterLabel(); label != "stage judge" {
				t.Fatalf("label %q", label)
			}
			if cmd := m.serveEnter(); cmd != nil {
				t.Fatal("selection launched activation")
			}
			if !reflect.DeepEqual(m.Cfg.Decision, before) || m.Cfg.Model != worker || len(m.Serve.Spawned) != 0 {
				t.Fatal("staging changed active selection or launched a server")
			}
			draft := m.Settings.DecisionDraft
			if draft == nil || draft.Model != "local candidate" || draft.Path != path || draft.Revision != "" || draft.Runtime != runtime || !draft.TrustRemoteCode {
				t.Fatalf("draft %+v", draft)
			}
			if !strings.Contains(m.Settings.DecisionNotice, "trust_remote_code=true") || !strings.Contains(m.Settings.DecisionNotice, "compatibility unconfirmed") {
				t.Fatal(m.Settings.DecisionNotice)
			}
			if cmd := m.serveUnload(); cmd != nil || m.Settings.DecisionDraft != draft {
				t.Fatal("candidate attempted unloading")
			}
			if cmd := m.applyDecisionSelection(); cmd != nil {
				t.Fatal("external selection attempted activation")
			}
			if !strings.Contains(m.Settings.DecisionNotice, "pending external activation") {
				t.Fatal(m.Settings.DecisionNotice)
			}
			m.saveSettings()
			saved, _, err := config.Load(m.CfgPath)
			if err != nil || saved.Decision.Path != path || saved.Decision.Runtime != runtime || saved.Decision.Revision != "" {
				t.Fatalf("saved %+v: %v", saved.Decision, err)
			}
		})
	}
}

func TestNativeJudgeCandidateRefusesStaleRuntimeAndInvalidConfig(t *testing.T) {
	m := settingsModel(t)
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.Serve.Rows = []ServeRow{{Sidecar: true, IsModel: true, Asset: &serve.Asset{ID: "org/model", Path: path, Revision: "commit", Format: "safetensors", Complete: true, Runtime: "laya"}}}
	before := m.Cfg.Decision
	m.Cfg.Decision.Runtime = "system_one"
	m.serveUseJudge()
	if m.Settings.DecisionDraft != nil {
		t.Fatal("stale native row changed selected runtime")
	}
	m.Cfg.Decision = before
	m.Cfg.Decision.TimeoutSeconds = -1
	m.serveUseJudge()
	if m.Settings.DecisionDraft != nil {
		t.Fatal("invalid config staged")
	}
	m.Cfg.Decision = before
	m.serveUseJudge()
	if m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Revision != "commit" {
		t.Fatal("snapshot revision not staged")
	}
}

func TestNativeJudgeCandidateUsesOwnedActivationRoute(t *testing.T) {
	m := settingsModel(t)
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := serve.Start(serve.Recipe{Name: "native-catalog-owner", Argv: napCmd("30")}, filepath.Join(t.TempDir(), "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	m.TrackSidecar(p, serve.Health{Decision: &decision.Health{PID: p.PID()}})
	m.Serve.Rows = append(m.Serve.Rows, ServeRow{Sidecar: true, IsModel: true, Asset: &serve.Asset{ID: "org/native", Path: path, Format: "safetensors", Complete: true, Runtime: m.Cfg.Decision.Runtime}})
	m.Serve.Sel = 1
	m.serveUseJudge()
	if m.Settings.DecisionDraft == nil {
		t.Fatal("not staged")
	}
	if cmd := m.applyDecisionSelection(); cmd == nil || !m.Settings.Applying {
		t.Fatal("catalog bypassed owned admission")
	}
	if m.Cfg.Decision.Model == "org/native" {
		t.Fatal("unexecuted activation committed selection")
	}
}

func TestSelectedWorkerAPIBaseRetainsOwnedRecipeIdentity(t *testing.T) {
	isolateModelRoots(t)
	m := settingsModel(t)
	root := "http://127.0.0.1:18889/custom"
	m.Cfg.Model.Server = root + "/v1"
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom-worker", Kind: "openai", URL: root, Argv: []string{"unstarted", "{model}"}}}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	var found []ServeRow
	for _, r := range got.rows {
		if !r.IsModel && strings.HasPrefix(r.Ep.URL, root) {
			found = append(found, r)
		}
	}
	if len(found) != 1 || found[0].Recipe == nil || found[0].Ep.URL != root || !found[0].Worker {
		t.Fatalf("worker recipe identity lost: %+v", found)
	}
}

func TestNormalizedWorkerSelectionRetainsOwnedRelease(t *testing.T) {
	isolateModelRoots(t)
	m := settingsModel(t)
	root := "http://127.0.0.1:18889/custom"
	m.Cfg.Model.Server = root + "/v1"
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom-worker", Kind: "openai", URL: root, Argv: napCmd("30")}}
	p, err := serve.Start(serve.Recipe{Name: "ownership-fixture", Argv: napCmd("30")}, filepath.Join(t.TempDir(), "owner.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	m.Serve.Spawned = map[string]*serve.Proc{root: p}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	for _, row := range got.rows {
		if row.Worker && !row.IsModel {
			if row.Ep.URL != root || !row.Ours {
				t.Fatalf("owned lifecycle key lost: %+v", row)
			}
			m.Serve.Rows, m.Serve.Sel = []ServeRow{row}, 0
			cmd := m.serveUnload()
			if cmd == nil {
				t.Fatal("no owned release")
			}
			m.onServeLoad(cmd().(serveLoadMsg))
			if p.PID() != 0 || m.Serve.Spawned[root] != nil {
				t.Fatal("owned process not released")
			}
			return
		}
	}
	t.Fatal("worker row absent")
}

func TestServeURLRetainsCustomPrefixes(t *testing.T) {
	for _, pair := range [][2]string{{"http://localhost:88/custom", "http://localhost:88/other/v1"}, {"http://localhost:88/custom/v1", "http://localhost:88/v1"}, {"http://localhost:88", "http://localhost:89/v1"}} {
		if sameServeURL(pair[0], pair[1]) {
			t.Fatalf("distinct endpoints merged %v", pair)
		}
	}
}

func TestWorkerCatalogSelectionRollsBackOnSaveFailure(t *testing.T) {
	m := serveModelWithModels(t)
	m.CfgPath = t.TempDir()
	m.Serve.Sel = 2
	before, server, name, session := m.Cfg.Model, m.Server, m.ModelName, m.St.Model
	m.serveUse()
	if m.Cfg.Model != before || m.Server != server || m.ModelName != name || m.St.Model != session {
		t.Fatal("failed save changed worker selection or derived state")
	}
}

func TestOrdinaryCatalogStagesJudge(t *testing.T) {
	m := serveModelWithModels(t)
	m.Serve.Sel = 2
	before := m.Cfg
	m.serveUseJudge()
	if !reflect.DeepEqual(m.Cfg, before) || m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Runtime != "ollama" || !strings.Contains(lastAlert(m).Text, "self-reported") {
		t.Fatal("ordinary catalog failed to stage a chat judge without changing live configuration")
	}
	m.serveUse()
	if m.Cfg.Model.Name != "qwen3:8b" {
		t.Fatal("worker catalog selection was affected")
	}
}

func TestSettingsStagesChatJudgeWithoutChangingLiveSelection(t *testing.T) {
	m := settingsModel(t)
	before := m.Cfg.Decision
	m.applyField(settingsField("decision", "runtime", "runtime"), "openai")
	if !reflect.DeepEqual(m.Cfg.Decision, before) || m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Runtime != "openai" {
		t.Fatal("chat judge failed to stage independently")
	}
}
