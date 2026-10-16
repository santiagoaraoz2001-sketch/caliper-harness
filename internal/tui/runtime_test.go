package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
	"strings"
	"testing"
)

func TestRuntimeSettingsPickerPersistAndRefusal(t *testing.T) {
	m := settingsModel(t)
	m.Tabs[m.Active].at.view = ViewSettings
	sel := -1
	for i, f := range settingFields() {
		if f.Section == "model" && f.Key == "runtime" {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatal("runtime not available in Settings")
	}
	m.Settings.Sel = sel
	for _, want := range []string{"ollama", "mlx", "rulith", "openai"} {
		out, _, handled := m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
		if !handled {
			t.Fatal("runtime enter not handled")
		}
		m = out.(Model)
		if m.Cfg.Model.Runtime != want {
			t.Fatalf("picker %q want %q", m.Cfg.Model.Runtime, want)
		}
	}
	m.applyField(settingsField("model", "runtime", "runtime"), "mlx")
	m.saveSettings()
	back, _, err := config.Load(m.CfgPath)
	if err != nil || back.Model.Runtime != "mlx" {
		t.Fatalf("runtime lost on save: %q %v", back.Model.Runtime, err)
	}
	m.applyField(settingsField("model", "runtime", "runtime"), "auto")
	if m.Cfg.Model.Runtime != "mlx" || !strings.Contains(lastAlert(m).Text, "model.runtime") {
		t.Fatalf("invalid runtime changed config/unlabelled: %+v %s", m.Cfg.Model, lastAlert(m).Text)
	}
}
func TestRuntimeStatusSaysRequestedAndSettingsDisclosesUnknown(t *testing.T) {
	s := Status{Think: false}
	if !strings.Contains(s.Render(), "OFF requested") {
		t.Fatal("footer OFF falsely suggests effective state")
	}
	m := settingsModel(t)
	m.Cfg.Params.Thinking = false
	v := m.renderSettings(180, 60)
	for _, want := range []string{"thinking requested", "unknown", "not applied", "generic compatibility"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q: %s", want, v)
		}
	}
	m.Cfg.Model.Runtime = "mlx"
	if !strings.Contains(m.renderSettings(180, 60), "template support unconfirmed") {
		t.Fatal("MLX thinking falsely confirmed")
	}
}
func TestRuntimeServeSelectionDoesNotGuessFromLabel(t *testing.T) {
	cases := []struct {
		name string
		row  ServeRow
		want string
	}{
		{"unowned mlx recipe", ServeRow{Ep: serve.Endpoint{URL: "http://external.invalid/v1", Label: "mlx"}, Have: true, Health: serve.Health{Up: true, Kind: "openai"}, Recipe: &serve.Recipe{Name: "mlx", Runtime: "mlx"}}, "openai"},
		{"unproven owned mlx recipe", ServeRow{Ep: serve.Endpoint{URL: "http://owned.invalid/v1", Label: "custom"}, Have: true, Ours: true, Health: serve.Health{Up: true, Kind: "openai"}, Recipe: &serve.Recipe{Name: "unrelated", Runtime: "mlx"}}, "openai"},
		{"validated native", ServeRow{Ep: serve.Endpoint{URL: "http://native.invalid/v1", Model: "chosen"}, Have: true, IsModel: true, Health: serve.Health{Up: true, Kind: "ollama", Runtime: "ollama"}}, "ollama"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := settingsModel(t)
			m.Cfg.Model.Runtime = "mlx"
			m.Serve.Rows = []ServeRow{tc.row}
			m.serveUse()
			back, _, err := config.Load(m.CfgPath)
			if err != nil || back.Model.Runtime != tc.want || m.Cfg.Model.Runtime != tc.want {
				t.Fatalf("selection runtime lost/guessed: %+v %+v %v", m.Cfg.Model, back.Model, err)
			}
		})
	}
	m := settingsModel(t)
	m.Cfg.Model.Runtime = "mlx"
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{URL: m.Cfg.Model.Server}, Have: true, Health: serve.Health{Up: true, Kind: "openai"}}}
	m.serveUse()
	if m.Cfg.Model.Runtime != "mlx" {
		t.Fatal("explicit active selection discarded")
	}
}
func TestRuntimeNativeDiscoveredModelRowsRetainDescriptor(t *testing.T) {
	m := settingsModel(t)
	m.CfgPath = filepath.Join(t.TempDir(), "runtime.yaml")
	msg := m.probeServeWith(probeClient())().(serveProbedMsg)
	found := false
	for _, r := range msg.rows {
		if r.IsModel && r.Asset == nil {
			found = true
			if r.Health.Runtime != "ollama" {
				t.Fatalf("native model child lost descriptor: %+v", r.Health)
			}
		}
	}
	if !found {
		t.Fatal("no component model rows")
	}
}
