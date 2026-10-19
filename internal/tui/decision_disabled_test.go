package tui

import (
	"specific-harness/internal/serve"
	"strings"
	"testing"
)

func TestSettingsDisableKeepsSelectionAndSkipsDecisionEndpoints(t *testing.T) {
	m := settingsModel(t)
	selected := m.Cfg.Decision
	m.applyField(settingsField("decision", "disabled", "bool"), "true")
	if !m.Cfg.Decision.Disabled || m.Cfg.Decision.Model != selected.Model || m.Cfg.Decision.Server != selected.Server {
		t.Fatal("toggle lost selection")
	}
	for _, ep := range m.serveEndpoints() {
		if ep.Kind == serve.KindSidecar || sameServeURL(ep.URL, selected.Server) {
			t.Fatalf("disabled endpoint probed: %+v", ep)
		}
	}
	for _, rec := range m.serveRecipes() {
		if rec.Kind == serve.KindSidecar {
			t.Fatalf("disabled recipe: %+v", rec)
		}
	}
	m.applyField(settingsField("decision", "model", "text"), "saved-but-off")
	if cmd := m.applyDecisionSelection(); cmd != nil {
		t.Fatal("disabled selection activated")
	}
	if m.Cfg.Decision.Model != "saved-but-off" || !m.Cfg.Decision.Disabled {
		t.Fatal("selection not retained")
	}
	m.applyField(settingsField("decision", "disabled", "bool"), "false")
	if m.Cfg.Decision.Disabled || m.Cfg.Decision.Model != "saved-but-off" {
		t.Fatal("enable lost selection")
	}
}

func TestDecisionDisabledSetupPresentationAndRetainedDraft(t *testing.T) {
	m := settingsModel(t)
	m.Setup.Started = true
	m.Setup.Judge = m.Cfg.Decision
	m.applyField(settingsField("decision", "disabled", "bool"), "true")
	if !m.Setup.Judge.Disabled {
		t.Fatal("retained Setup draft would reenable disabled judge")
	}
	rows := strings.Join(m.Setup.reviewRows(120), "\n")
	if !strings.Contains(rows, "disabled; required decisions ask you") {
		t.Fatalf("setup review hides disabled state: %s", rows)
	}
	m.Setup.Stage = 1
	if view := m.renderSetup(120, 20); !strings.Contains(view, "Decision model disabled") {
		t.Fatalf("setup selection claims active judge: %s", view)
	}
}
