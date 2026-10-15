package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// settingsModel builds a chat-ready model with a temp config path.
func settingsModel(t *testing.T) Model {
	t.Helper()
	m := New(testCfg(), "test")
	m.CfgPath = filepath.Join(t.TempDir(), "harness.yaml")
	return m
}

func settingsField(section, key, kind string) SettingField {
	return SettingField{Section: section, Key: key, Label: key, Kind: kind}
}

func TestSettingsAuthCyclesAllModes(t *testing.T) {
	m := settingsModel(t)
	f := settingsField("auth", "mode", "auth")
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		m.cycleField(f)
		seen[m.Cfg.Auth.Mode] = true
	}
	for _, want := range []string{"yolo", "authorize-risky", "authorize-all"} {
		if !seen[want] {
			t.Errorf("auth cycle never reached %q: %v", want, seen)
		}
	}
	if m.Cfg.Auth.Mode != testCfg().Auth.Mode {
		t.Errorf("3 cycles must return to start: got %q", m.Cfg.Auth.Mode)
	}
}

func TestSettingsDeviceAndSubmodeCycle(t *testing.T) {
	m := settingsModel(t)
	m.cycleField(settingsField("decision", "device", "device"))
	if m.Cfg.Decision.Device != "auto" || m.Settings.DecisionDraft.Device != "cpu" {
		t.Errorf("device must stage cpu while requested remains auto")
	}
	m.cycleField(settingsField("subagents", "mode", "submode"))
	if m.Cfg.Subagents.Mode != "sequential" {
		t.Errorf("submode concurrent->%q, want sequential", m.Cfg.Subagents.Mode)
	}
	m.cycleField(settingsField("subagents", "mode", "submode"))
	if m.Cfg.Subagents.Mode != "concurrent" {
		t.Errorf("second cycle must return to concurrent, got %q", m.Cfg.Subagents.Mode)
	}
}

func TestSettingsRefusesBadNumber(t *testing.T) {
	m := settingsModel(t)
	before := m.Cfg.Params.Temperature
	m.applyField(settingsField("params", "temperature", "float"), "hot")
	if m.Cfg.Params.Temperature != before {
		t.Error("bad value must not touch the working config")
	}
	a := lastAlert(m)
	if a.Kind != "err" || !strings.Contains(a.Text, "temperature") {
		t.Errorf("refusal must name the field: %+v", a)
	}
}

func TestSettingsRefuseAllValidationError(t *testing.T) {
	m := settingsModel(t)
	before := m.Cfg.Workflows.MaxRetries
	m.applyField(settingsField("workflows", "max_retries", "int"), "-5")
	if m.Cfg.Workflows.MaxRetries != before {
		t.Error("invalid value must leave the config untouched")
	}
	if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("want err alert, got %+v", a)
	}
}

func TestSettingsApplyGoesLive(t *testing.T) {
	m := settingsModel(t)
	m.applyField(settingsField("params", "temperature", "float"), "0.3")
	if m.Cfg.Params.Temperature != 0.3 {
		t.Errorf("temperature=%v, want 0.3", m.Cfg.Params.Temperature)
	}
	m.applyField(settingsField("model", "server", "text"), "http://x:9/v1")
	if m.Server != "http://x:9/v1" {
		t.Errorf("derived server=%q, want live value", m.Server)
	}
	m.applyField(settingsField("params", "thinking", "bool"), "false")
	if m.Cfg.Params.Thinking {
		t.Error("thinking=false must apply")
	}
}

func TestSettingsSaveRoundTrips(t *testing.T) {
	m := settingsModel(t)
	m.applyField(settingsField("params", "temperature", "float"), "0.3")
	m.saveSettings()
	raw, err := os.ReadFile(m.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	params, _ := back["params"].(map[string]any)
	if params["temperature"] != 0.3 {
		t.Errorf("saved temperature=%v", params["temperature"])
	}
}

func TestSettingsArmedEnterApplies(t *testing.T) {
	m := settingsModel(t)
	m = submit(m, "/settings")
	if !m.settingsActive() {
		t.Fatal("not on the settings view")
	}
	// Sel 0 is model/server (text): enter arms, typing + enter applies.
	m = key(m, "enter")
	if m.Settings.Editing == nil {
		t.Fatal("enter must arm the text field")
	}
	m = submit(m, "http://armed:9/v1")
	if m.Settings.Editing != nil {
		t.Error("submit must disarm after applying")
	}
	if m.Server != "http://armed:9/v1" {
		t.Errorf("server=%q, want the armed value live", m.Server)
	}
}

func TestSettingsEscCancelsArmedEdit(t *testing.T) {
	m := settingsModel(t)
	m = submit(m, "/settings")
	m = key(m, "enter")
	if m.Settings.Editing == nil {
		t.Fatal("enter must arm the text field")
	}
	before := m.Cfg.Model.Server
	m = key(m, "esc")
	if m.Settings.Editing != nil {
		t.Error("esc must disarm without applying")
	}
	if m.Cfg.Model.Server != before {
		t.Error("esc-cancelled edit must not touch the config")
	}
}

func TestSettingsSlashDisarmsArmedEdit(t *testing.T) {
	m := settingsModel(t)
	m = submit(m, "/settings")
	m = key(m, "enter")
	m = submit(m, "/status")
	if m.Settings.Editing != nil {
		t.Error("a slash command must disarm the pending edit")
	}
	if !m.serveActive() {
		t.Error("the slash command must still run")
	}
}
