package tui

import (
	"strings"
	"testing"
)

func TestOllamaNativeProtocolSettingsAndDisclosure(t *testing.T) {
	m := settingsModel(t)
	m.Cfg.Decision.Runtime = "ollama"
	m.Cfg.Decision.Device = "auto"
	m.applyField(settingsField("decision", "protocol", "text"), "system_one")
	if m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Protocol != "system_one" {
		t.Fatal("protocol was not staged", lastAlert(m))
	}
	m.applyDecisionSelection()
	if m.Cfg.Decision.Protocol != "system_one" || !strings.Contains(m.Settings.DecisionNotice, "native option probabilities") || strings.Contains(m.Settings.DecisionNotice, "self-reported") {
		t.Fatal(m.Settings.DecisionNotice)
	}
	if got := m.settingValue(settingsField("decision", "protocol", "text")); got != "system_one" {
		t.Fatal(got)
	}
	m.applyField(settingsField("decision", "runtime", "runtime"), "openai")
	if m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Protocol != "" {
		t.Fatal("stale Ollama protocol survived a runtime change")
	}
}
