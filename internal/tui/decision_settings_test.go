package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/serve"
	"strings"
	"sync"
	"testing"
)

func TestDecisionSettingsDoesNotClaimImmediateLive(t *testing.T) {
	m := settingsModel(t)
	m.applyField(settingsField("decision", "model", "text"), "unlisted/preferred")
	if strings.Contains(lastAlert(m).Text, " live ") {
		t.Fatal("unactivated decision selection claimed live")
	}
}

// A short-lived sleep child and HTTP double exercise PID ownership without model inference.
func TestDecisionOwnedApplyFailureSuccessAndStale(t *testing.T) {
	for _, mode := range []string{"reject", "success", "stale", "pid-skew", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			m := settingsModel(t)
			p, err := serve.Start(serve.Recipe{Name: "owner-double", Argv: napCmd("30")}, filepath.Join(t.TempDir(), "owner.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			pid := p.PID()
			old := m.Cfg.Decision
			yes, no := true, false
			makeHealth := func(s decision.Selection) decision.Health {
				return decision.Health{Status: "ok", PID: pid, Loaded: &yes, Ready: &yes, Loading: &no, Model: &s.Model, Requested: &s, Backends: []string{"cpu"}, Active: &decision.Identity{Model: s.Model, Runtime: s.Runtime, Source: "path", Path: "/resolved", Device: "cpu", NativeDevice: "cpu", Architecture: "native", Parameters: 100, Selection: s}}
			}
			var fixtureMu sync.Mutex
			posts := 0
			current := decision.SelectionFor(old)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixtureMu.Lock()
				defer fixtureMu.Unlock()
				if r.URL.Path == "/activate" {
					posts++
					if mode == "reject" {
						w.WriteHeader(500)
						fmt.Fprint(w, `{"error":"bad candidate; old preserved"}`)
						return
					}
					if err := json.NewDecoder(r.Body).Decode(&current); err != nil {
						t.Error(err)
					}
					if mode == "lost-response" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
				}
				h := makeHealth(current)
				if mode == "pid-skew" {
					h.PID++
				}
				json.NewEncoder(w).Encode(h)
			}))
			defer server.Close()
			m.Cfg.Decision.Server = server.URL
			old.Server = server.URL
			if len(m.Serve.Rows) != 0 {
				t.Fatal("fresh Settings already has Status results")
			}
			h := makeHealth(current)
			m.TrackSidecar(p, serve.Health{Up: true, Decision: &h})
			if !m.Serve.Rows[0].Ours {
				t.Fatal("initial owned identity not shown")
			}
			m.applyField(SettingField{"decision", "model", "decision model", "text"}, "arbitrary-compatible")
			cmd := m.applyDecisionSelection()
			if cmd == nil {
				t.Fatal("owned selection did not start")
			}
			m.Cfg.Params.Temperature = .31 // unrelated live edit must survive receipt
			msg := cmd().(serveProbedMsg)
			if mode == "stale" {
				m.applyField(SettingField{"decision", "model", "decision model", "text"}, "later-candidate")
			}
			m.onServeProbed(msg)
			if p.PID() != pid || m.Serve.Spawned[server.URL] != p {
				t.Fatal("activation discarded good process")
			}
			if m.Cfg.Params.Temperature != .31 {
				t.Fatal("whole-config stale overwrite")
			}
			if mode == "success" {
				if m.Cfg.Decision.Model != "arbitrary-compatible" || m.Settings.DecisionDraft != nil {
					t.Fatal("success not admitted")
				}
			} else {
				if m.Cfg.Decision.Model != old.Model || m.Settings.DecisionDraft == nil {
					t.Fatal("unconfirmed selection committed")
				}
			}
			fixtureMu.Lock()
			defer fixtureMu.Unlock()
			if mode == "lost-response" && (posts != 1 || !strings.Contains(m.Settings.DecisionNotice, "not confirmed")) {
				t.Fatal("lost activation response retried or claimed settled")
			}
			if mode == "pid-skew" && posts != 0 {
				t.Fatal("PID-skewed external process activated")
			}
		})
	}
}
func TestDecisionExternalSaveZeroActivationAndReopen(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	m := settingsModel(t)
	m.Cfg.Decision.Server = server.URL
	for key, val := range map[string]string{"model": "third/model", "runtime": "system_one", "path": "/downloaded/native", "revision": "pin", "trust_remote_code": "true"} {
		kind := "text"
		if key == "trust_remote_code" {
			kind = "bool"
		}
		m.applyField(SettingField{"decision", key, key, kind}, val)
	}
	if cmd := m.applyDecisionSelection(); cmd != nil {
		t.Fatal("external activation command produced")
	}
	m.saveSettings()
	got, _, err := config.Load(m.CfgPath)
	if err != nil || got.Decision.Model != "third/model" || got.Decision.Runtime != "system_one" || got.Decision.Path != "/downloaded/native" || !got.Decision.TrustRemoteCode || calls != 0 {
		t.Fatalf("requested save calls%d cfg%+v err%v", calls, got.Decision, err)
	}
	if !strings.Contains(lastAlert(m).Text, "pending external activation") {
		t.Fatal(lastAlert(m).Text)
	}
}

func TestDecisionStartOccupiedAndCustomRecipePreserved(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	m.Cfg.Serve.Recipes = []config.RecipeConfig{{Name: "sidecar", Kind: serve.KindSidecar, URL: m.Cfg.Decision.Server, Argv: []string{"/chosen/python", "/chosen/server.py"}}}
	rec := m.recipeFor(&m.Serve.Rows[2])
	if rec == nil || rec.Argv[0] != "/chosen/python" {
		t.Fatal("custom recipe overwritten")
	}
	m.Serve.Rows[2].Health.Reachable = true
	m.serveStart()
	if len(m.Serve.Spawned) != 0 || !strings.Contains(lastAlert(m).Text, "no second process") {
		t.Fatal("occupied invalid endpoint was started")
	}
}

func TestDecisionStatusActualAndRequestedIdentity(t *testing.T) {
	m := serveModel(t)
	m.Serve.Sel = 2
	yes, no := true, false
	model := "actual/native"
	h := decision.Health{Status: "ok", PID: 123, Loaded: &yes, Ready: &yes, Loading: &no, Model: &model, Active: &decision.Identity{Model: model, Runtime: "system_one", Source: "third/source", Path: "/resolved-assets", Architecture: "D1Model", Revision: nil, NativeDevice: "mps:0", Device: "gpu", Parameters: 3123483888}}
	m.Serve.Rows[2].Health = serve.Health{Up: true, Decision: &h}
	rendered := viewportServePages(m, 180, 30)
	for _, want := range []string{"actual/native", "system_one/mps:0", "3123483888", "D1Model", "third/source", "/resolved-assets", "requested:", "typed-decisions"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing identity %q", want)
		}
	}
}
