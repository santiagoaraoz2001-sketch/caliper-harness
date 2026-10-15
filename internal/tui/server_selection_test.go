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
	"time"
)

func TestCatalogJudgeSelectionPersistsWithoutNativeActivation(t *testing.T) {
	m := serveModelWithModels(t)
	m.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	m.Serve.Sel = 2
	worker := m.Cfg.Model
	m.serveUseJudge()
	if m.Cfg.Model != worker || m.Cfg.Decision.Runtime != "openai" || m.Cfg.Decision.Model != "qwen3:8b" || m.Cfg.Decision.Server != serve.OllamaURL {
		t.Fatalf("selection %+v", m.Cfg.Decision)
	}
	saved, _, err := config.Load(m.CfgPath)
	if err != nil || saved.Decision.Model != "qwen3:8b" {
		t.Fatalf("saved %+v %v", saved.Decision, err)
	}
	for _, ep := range m.serveEndpoints() {
		if ep.URL == m.Cfg.Decision.Server && ep.Kind == serve.KindSidecar {
			t.Fatal("chat judge probed as native sidecar")
		}
	}
	if m.decisionOwner() != nil || !strings.Contains(lastAlert(m).Text, "next run") {
		t.Fatal("claimed native activation")
	}
}

func TestCatalogJudgeSelectionRollsBackOnSaveFailure(t *testing.T) {
	m := serveModelWithModels(t)
	m.CfgPath = t.TempDir() // directory cannot be replaced as YAML
	m.Serve.Sel = 2
	old := m.Cfg
	draft := m.Cfg.Decision
	m.Settings.DecisionDraft, m.Settings.DecisionSeq, m.Settings.DecisionNotice = &draft, 7, "previous notice"
	m.serveUseJudge()
	if !reflect.DeepEqual(m.Cfg, old) || m.Settings.DecisionDraft != &draft || m.Settings.DecisionSeq != 7 || m.Settings.DecisionNotice != "previous notice" {
		t.Fatal("failed save changed selection or staging state")
	}
}

func TestGenericDecisionSettingsApplyDoesNotCallNativeActivation(t *testing.T) {
	m := settingsModel(t)
	m.applyField(settingsField("decision", "runtime", "runtime"), "openai")
	if cmd := m.applyDecisionSelection(); cmd != nil {
		t.Fatal("generic transport attempted activation")
	}
	if m.Cfg.Decision.Runtime != "openai" || !strings.Contains(m.Settings.DecisionNotice, "next run") {
		t.Fatalf("selection %+v notice %s", m.Cfg.Decision, m.Settings.DecisionNotice)
	}
}

func TestGGUFCandidateListedForEachConfiguredServer(t *testing.T) {
	isolateModelRoots(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("GGUFfixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m := settingsModel(t)
	m.Cfg.Serve.ModelDirs = []string{dir}
	for _, name := range []string{"first", "second"} {
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
			m.Cfg.Decision.Runtime = "openai"
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
			if row.Resident || row.Ours || row.Asset.Runtime != runtime || row.Ep.Model != path {
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
	m.Cfg.Decision.Runtime = "openai"
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
	p, err := serve.Start(serve.Recipe{Name: "native-catalog-owner", Argv: []string{"sleep", "30"}}, filepath.Join(t.TempDir(), "owner.log"))
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

func TestCatalogJudgeSelectionInvalidatesPendingNativeReceipt(t *testing.T) {
	m := serveModelWithModels(t)
	m.CfgPath = filepath.Join(t.TempDir(), "config.yaml")
	m.Serve.Sel = 2
	old, seq := m.Cfg.Decision, m.Settings.DecisionSeq
	m.Settings.Applying = true
	m.serveUseJudge()
	selected := m.Cfg.Decision
	m.onServeProbed(serveProbedMsg{application: &decisionApplied{seq: seq, cfg: old, health: decision.Health{Active: &decision.Identity{NativeDevice: "cpu"}}}})
	if !reflect.DeepEqual(m.Cfg.Decision, selected) || m.Cfg.Decision.Runtime != "openai" || !strings.Contains(m.Settings.DecisionNotice, "stale") {
		t.Fatalf("old activation replaced new catalog selection: %+v / %s", m.Cfg.Decision, m.Settings.DecisionNotice)
	}
}

func TestSelectedJudgeBaseRetainsLaunchRecipeAndRole(t *testing.T) {
	isolateModelRoots(t)
	m := settingsModel(t)
	m.Cfg.Decision.Runtime, m.Cfg.Decision.Model = "openai", "judge-choice"
	m.Cfg.Decision.Server = "http://127.0.0.1:18888/custom/v1"
	m.Cfg.Model.Name = "worker-choice"
	root := "http://127.0.0.1:18888/custom"
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom-judge", Kind: "openai", URL: root, Argv: []string{"/bin/sh", "-c", `printf '%s' "$1" > "$2"`, "fixture", "{model}", filepath.Join(t.TempDir(), "argv")}}}
	got := m.probeServeWith(probeClient())().(serveProbedMsg)
	var found []ServeRow
	for _, r := range got.rows {
		if !r.IsModel && strings.HasPrefix(r.Ep.URL, root) {
			found = append(found, r)
		}
	}
	if len(found) != 1 || found[0].Recipe == nil {
		t.Fatalf("selected judge split/lost recipe: %+v", found)
	}
	found[0].Health = serve.Health{} // Server stopped after catalog selection.
	m.Serve.Rows, m.Serve.Sel = found, 0
	m.serveStart()
	p := m.Serve.Spawned[found[0].Ep.URL]
	if p == nil {
		t.Fatalf("not started: %s", lastAlert(m).Text)
	}
	defer p.Stop()
	out := m.Cfg.Serve.Recipes[0].Argv[5]
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(out)
		if err == nil && len(b) > 0 {
			if string(b) != "judge-choice" {
				t.Fatalf("launched wrong role model %q", b)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fixture did not capture argv")
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
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "custom-worker", Kind: "openai", URL: root, Argv: []string{"sleep", "30"}}}
	p, err := serve.Start(serve.Recipe{Name: "ownership-fixture", Argv: []string{"sleep", "30"}}, filepath.Join(t.TempDir(), "owner.log"))
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
