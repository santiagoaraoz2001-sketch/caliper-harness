package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/config"
	"specific-harness/internal/serve"
)

func r3JudgeSelectionModel(t *testing.T, runtime string) Model {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	m := settingsModel(t)
	m.Width, m.Height = 160, 40
	m.Cfg.Decision.Runtime, m.Cfg.Decision.Protocol = "ollama", "system_one"
	m.Cfg.Decision.Model, m.Cfg.Decision.Server = "d1:3b", "http://127.0.0.1:11434"
	m.Cfg.Decision.Path, m.Cfg.Decision.Revision = "", ""
	m.Cfg.Decision.Device, m.Cfg.Decision.TrustRemoteCode = "auto", false
	m.Cfg.Decision.Disabled = false
	model := "generic-judge"
	if runtime == "ollama" {
		model = "d1:3b"
	}
	m.Serve.Rows = []ServeRow{{IsModel: true, Have: true, Ep: serve.Endpoint{URL: "http://127.0.0.1:55371/v1", Model: model}, Health: serve.Health{Up: true, Runtime: runtime, APIBase: "http://127.0.0.1:55371/v1"}}}
	return m
}

func TestR3ManageJudgeRuntimeApplySaveReopen(t *testing.T) {
	for _, runtime := range []string{"openai", "ollama"} {
		t.Run(runtime, func(t *testing.T) {
			m := r3JudgeSelectionModel(t, runtime)
			old := m.Cfg.Decision
			m.serveUseJudge()
			protocol := ""
			if runtime == "ollama" {
				protocol = "system_one"
			}
			if !reflect.DeepEqual(m.Cfg.Decision, old) || m.Settings.DecisionDraft == nil || m.Settings.DecisionDraft.Runtime != runtime || m.Settings.DecisionDraft.Protocol != protocol {
				t.Fatalf("selection failed or prematurely changed live identity: draft=%+v alert=%+v", m.Settings.DecisionDraft, lastAlert(m))
			}
			if cmd := m.applyDecisionSelection(); cmd != nil {
				t.Fatal("external selection attempted runtime activation")
			}
			m.saveSettings()
			reopened, _, err := config.LoadProject(m.CfgPath)
			if err != nil || reopened.Decision.Runtime != runtime || reopened.Decision.Protocol != protocol || reopened.Decision.Model != m.Serve.Rows[0].Ep.Model {
				t.Fatalf("selection failed save/reopen: %+v %v", reopened.Decision, err)
			}
			// Explicit incompatible author input must not alter a live identity, draft or file.
			if runtime == "openai" {
				raw, err := os.ReadFile(m.CfgPath)
				if err != nil {
					t.Fatal(err)
				}
				before := m.Cfg.Decision
				if m.applyField(settingsField("decision", "protocol", "text"), "system_one") || m.Settings.DecisionDraft != nil || !reflect.DeepEqual(before, m.Cfg.Decision) {
					t.Fatal("invalid explicit protocol changed selection")
				}
				after, err := os.ReadFile(m.CfgPath)
				if err != nil || string(raw) != string(after) {
					t.Fatal("invalid protocol changed saved bytes")
				}
			}
		})
	}
}

func TestR3SetupJudgeRuntimeSaveReopen(t *testing.T) {
	for _, runtime := range []string{"openai", "ollama"} {
		t.Run(runtime, func(t *testing.T) {
			m := r3JudgeSelectionModel(t, runtime)
			before := m.Cfg.Decision
			m.OpenSetup()
			m = viewportKey(m, tea.KeyEnter) // Coding selection.
			choices := m.currentSetupChoices()
			for _, choice := range choices {
				if choice.model.Name == m.Serve.Rows[0].Ep.Model {
					m.Setup.JudgePick = choice.key
				}
			}
			m = viewportKey(m, tea.KeyEnter) // Judge selection.
			protocol := ""
			if runtime == "ollama" {
				protocol = "system_one"
			}
			if m.Setup.Stage != 2 || m.Setup.Judge.Runtime != runtime || m.Setup.Judge.Protocol != protocol || !reflect.DeepEqual(before, m.Cfg.Decision) {
				t.Fatalf("Setup failed to stage coherent selection: %+v", m.Setup)
			}
			m = viewportKey(m, tea.KeyEnter) // Review.
			m = viewportKey(m, tea.KeyEnter) // Save.
			got, _, err := config.LoadProject(m.CfgPath)
			if err != nil || m.Setup.Err != "" || got.Decision.Runtime != runtime || got.Decision.Protocol != protocol {
				t.Fatalf("Setup save/reopen: %+v, %v, %s", got.Decision, err, m.Setup.Err)
			}
		})
	}
}

func TestR3StatusEscapeMatchesActiveOwner(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width, m.Height = 160, 40
	m.navigate(ViewStatus, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Chat.Running, m.Chat.cancel = true, cancel
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "esc stops run") || !strings.Contains(view, "/chat") || strings.Contains(strings.ToLower(view), "esc chat") {
		t.Fatalf("active status advertised wrong Escape: %s", view)
	}
	m = key(m, "esc")
	if ctx.Err() != context.Canceled || m.Tabs[m.Active].at.view != ViewStatus {
		t.Fatal("Escape no longer cancels the active run in place")
	}
}

func TestR3ContextCostKeepsCurrentAndPriorOwnersSeparate(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Context.Measured, m.Chat.Context.Prompt, m.Chat.Context.ModelKey = true, 1234, m.contextKey()
	m.Chat.LastCostKnown, m.Chat.LastUnknown = true, true
	m.Chat.LastIn, m.Chat.LastOut = 700, 40
	m.Chat.Running = true
	m.contextChat()
	text := lastAlert(m).Text
	if !strings.Contains(text, "prompt: 1.2k") || !strings.Contains(text, "current run cost available after settlement") || strings.Contains(text, "700") || strings.Contains(text, "unreported") || strings.Contains(text, "last-run cost") {
		t.Fatalf("mixed cost owners: %s", text)
	}
	m.Chat.Running = false
	m.contextChat()
	if text = lastAlert(m).Text; !strings.Contains(text, "700 in, 40 out") || !strings.Contains(text, "unreported") {
		t.Fatalf("idle partial prior totals lost: %s", text)
	}
	m.Chat.LastIn, m.Chat.LastOut = 0, 0
	m.contextChat()
	if text = lastAlert(m).Text; !strings.Contains(text, "last-run cost: unknown") || strings.Contains(text, "0 in, 0 out") {
		t.Fatalf("unreported usage represented as zero: %s", text)
	}
	m.Chat.LastUnknown = false
	m.contextChat()
	if !strings.Contains(lastAlert(m).Text, "0 in, 0 out") {
		t.Fatal("known zero no longer represented faithfully")
	}
}

func TestR3SettingsHeaderDistinguishesAvailability(t *testing.T) {
	m := settingsModel(t)
	view := ansi.Strip(m.renderSettings(160, 40))
	if !strings.Contains(view, "decision selection staged") || !strings.Contains(view, "availability immediately") || strings.Contains(view, "decision edits staged") {
		t.Fatalf("contradictory staged/availability guidance: %s", view)
	}
	before := m.Cfg.Decision.Model
	if !m.applyField(settingsField("decision", "disabled", "bool"), "true") || !m.Cfg.Decision.Disabled || m.Settings.DecisionDraft != nil || m.Cfg.Decision.Model != before {
		t.Fatal("availability exception no longer immediate or changed identity")
	}
}

func TestR3WorkflowsComposerPrecedesNavigation(t *testing.T) {
	m := New(testCfg(), "test")
	m.navigate(ViewWorkflows, "")
	m.Wf.Rows = []WfRow{{Approach: "selected-approach", Title: "selected"}}
	m.Input.SetValue("/tools coding")
	m = enter(m)
	if m.Input.Value() != "" || m.Chat.Context.ToolView != "coding" || m.Wf.Dive != "" || m.Tabs[m.Active].at.view != ViewWorkflows {
		t.Fatal("Workflows intercepted submitted command")
	}
	m.Input.SetValue("normal draft ")
	for _, letter := range []string{"l", "p", "c"} {
		m = key(m, letter)
	}
	if m.Input.Value() != "normal draft lpc" || m.Wf.ShowDone || m.Wf.Busy != "" || m.Wf.Dive != "" {
		t.Fatal("workflow action consumed composer text")
	}
	// Empty composer keeps the existing deliberate view shortcuts.
	m.Input.Reset()
	m = key(m, "c")
	if !m.Wf.ShowDone {
		t.Fatal("empty composer complete toggle no longer works")
	}
	m.Alerts = nil
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.Wf.Dive != "selected-approach" || m.Tabs[m.Active].at.view != ViewPlans || cmd == nil {
		t.Fatal("empty composer Enter no longer dives")
	}
}
