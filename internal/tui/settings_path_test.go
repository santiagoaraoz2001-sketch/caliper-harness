package tui

import (
	"strings"
	"testing"
)

func TestArmedSettingsLiteralPathsAndCommands(t *testing.T) {
	// Exact checkpoint path from root's failed native UI attempt; no files are opened here.
	const nativePath = "/Volumes/Cold Storage 1/SpecificHarnessModels/d1-3B-eval/models--LiquidAI--d1-3B/snapshots/051bcc464b01b9f92942b364d9586b0ef5912432"
	for _, path := range []string{nativePath, "/status/cache with spaces", "/home/é/native checkpoint"} {
		t.Run(path, func(t *testing.T) {
			m := settingsModel(t)
			m = submit(m, "/settings")
			for i, f := range settingFields() {
				if f.Section == "decision" && f.Key == "path" {
					m.Settings.Sel = i
				}
			}
			m = key(m, "enter")
			if m.Settings.Editing == nil || m.Settings.Editing.Key != "path" {
				t.Fatal("path edit not armed")
			}
			m = submit(m, path) // public Enter through Model.Update, not direct applyField
			if m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Path != path {
				t.Fatalf("literal path not staged: draft%+v alert%+v", m.Settings.DecisionDraft, lastAlert(m))
			}
			if m.Cfg.Decision.Path != "" || m.Settings.Editing != nil || !m.settingsActive() {
				t.Fatal("literal path must stage/disarm without navigation or config commit")
			}
		})
	}
	for _, command := range []string{"/status", "/auth risky", "/help"} {
		t.Run(command, func(t *testing.T) {
			m := settingsModel(t)
			m = submit(m, "/settings")
			m.armEdit(settingsField("decision", "path", "text"))
			m = submit(m, command)
			if m.Settings.Editing != nil || m.Settings.DecisionDraft != nil {
				t.Fatal("registered command did not disarm, or became path")
			}
			switch command {
			case "/status":
				if !m.serveActive() {
					t.Fatal("status command not dispatched")
				}
			case "/auth risky":
				if m.Cfg.Auth.Mode != "authorize-risky" {
					t.Fatal("auth argument lost")
				}
			case "/help":
				if !m.HelpOpen {
					t.Fatal("help did not dispatch")
				}
			}
		})
	}
	t.Run("unarmed unknown remains refused", func(t *testing.T) {
		m := settingsModel(t)
		m = submit(m, "/settings")
		m = submit(m, nativePath)
		if m.Settings.DecisionDraft != nil || !strings.Contains(lastAlert(m).Text, "unknown command") {
			t.Fatal("normal composer routing changed")
		}
	})
}
