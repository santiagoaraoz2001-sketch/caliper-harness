package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/config"
	"specific-harness/internal/hw"
	"specific-harness/internal/llm"
	"specific-harness/internal/run"
)

// SettingField is one editable row: section, key, label, and kind.
// Kinds: text, float, int, bool, auth, device, submode, runtime.
type SettingField struct {
	Section, Key, Label, Kind string
}

// settingFields is the curated form: chat-time knobs, not the whole
// YAML (serve/watch stay file-only — launch-time concerns).
func settingFields() []SettingField {
	fields := make([]SettingField, len(settingBindings))
	for i, binding := range settingBindings {
		fields[i] = binding.field
	}
	return fields
}

// SettingsState is the T6.10 form: selection plus one armed edit.
type SettingsState struct {
	Sel            int
	Editing        *SettingField
	Err            string
	DecisionDraft  *config.DecisionConfig
	DecisionSeq    int
	Applying       bool
	DecisionNotice string
}

func (m *Model) settingsActive() bool {
	return m.Tabs[m.Active].at.view == ViewSettings
}

// settingValue renders one field's live value from the working config.
func (m *Model) settingValue(f SettingField) string {
	c := m.Cfg
	if m.Settings.DecisionDraft != nil {
		c.Decision = *m.Settings.DecisionDraft
	}
	value := settingRead(settingTarget(&c, f))
	if f.Section == "model" && value == "" {
		switch f.Key {
		case "tool_protocol":
			return "structured"
		case "runtime":
			return llm.RuntimeOpenAI
		}
	}
	return value
}

// cycleNext returns the next enum value, wrapping around.
func cycleNext(options []string, cur string) string {
	for i, o := range options {
		if o == cur {
			return options[(i+1)%len(options)]
		}
	}
	return options[0]
}

var authModes = []string{config.AuthYOLO, config.AuthRisky, config.AuthAll}
var deviceModes = []string{"auto", "cpu", "gpu"}
var subagentModes = []string{config.SubagentsSequential, config.SubagentsConcurrent}

// cycleField steps bool/enum fields immediately: the picker the
// founder asked for, no invalid states possible.
func (m *Model) cycleField(f SettingField) {
	var next string
	switch f.Kind {
	case "protocol":
		next = cycleNext([]string{"structured", "text"}, m.settingValue(f))
	case "bool":
		if m.settingValue(f) == "true" {
			next = "false"
		} else {
			next = "true"
		}
	case "auth":
		next = cycleNext(authModes, m.settingValue(f))
	case "device":
		next = cycleNext(deviceModes, m.settingValue(f))
	case "runtime":
		options := []string{llm.RuntimeOpenAI, llm.RuntimeOllama, llm.RuntimeMLX, llm.RuntimeRulith}
		if f.Section == "decision" {
			options = []string{"laya", "system_one", "openai", "mlx", "ollama", "rulith"}
		}
		next = cycleNext(options, m.settingValue(f))
	case "submode":
		next = cycleNext(subagentModes, m.settingValue(f))
	default:
		return
	}
	m.applyField(f, next)
}

// applyField parses one value onto a config copy and validates the
// whole copy: bad values refuse loud with the error named, nothing
// written anywhere.
func (m *Model) applyField(f SettingField, val string) bool {
	if m.Tests.Busy != "" {
		m.alert("info", "change configuration after the test command settles")
		return false
	}
	next := m.Cfg
	if f.Section == "decision" && m.Settings.DecisionDraft != nil {
		next.Decision = *m.Settings.DecisionDraft
	}
	if settingTarget(&next, f) == nil {
		m.alert("err", fmt.Sprintf("unknown setting %s/%s", f.Section, f.Key))
		return false
	}
	err := settingWrite(&next, f, val)
	if err != nil {
		m.alert("err", fmt.Sprintf("%s.%s (%s) needs a number, got %q", f.Section, f.Key, f.Label, val))
		return false
	}
	if verr := next.Validate(); verr != nil {
		m.alert("err", fmt.Sprintf("refusing %s=%q: %s", f.Label, val, verr.Error()))
		return false
	}
	if f.Section == "decision" && f.Key == "disabled" {
		if m.Chat.Running || m.modelWorkActive() || m.Settings.Applying {
			m.alert("err", "wait for active work to settle before changing decision availability")
			return false
		}
		m.Cfg.Decision.Disabled = next.Decision.Disabled
		if m.Setup.Started {
			m.Setup.Judge.Disabled = next.Decision.Disabled
		}
		if m.Settings.DecisionDraft != nil {
			m.Settings.DecisionDraft.Disabled = next.Decision.Disabled
		}
		m.Settings.DecisionNotice = "decision availability changed for future runs; selected model preserved; s saves"
		m.alert("info", m.Settings.DecisionNotice)
		return true
	}
	if f.Section == "decision" {
		m.Settings.DecisionDraft = &next.Decision
		m.Settings.DecisionSeq++
		m.Settings.DecisionNotice = "staged — a applies; external native services remain pending activation"
		m.alert("info", m.Settings.DecisionNotice)
		return true
	}
	m.Cfg = next
	m.syncDerived()
	m.alert("info", fmt.Sprintf("%s live — s saves to file", f.Label))
	return true
}

// syncDerived re-resolves the working fields runs read directly:
// server, model, and the context window (autodetect included).
func (m *Model) syncDerived() {
	m.Server, m.ModelName = m.Cfg.Model.Server, m.Cfg.Model.Name
	m.St.Auth, m.St.Think, m.St.Model = ShortAuth(m.Cfg.Auth.Mode), m.Cfg.Params.Thinking, m.Cfg.Model.Name
	if w, err := hw.ResolveWindow(m.Cfg.Context.Window); err != nil {
		m.alert("err", fmt.Sprintf("window won't resolve: %s", err.Error()))
	} else {
		m.St.CtxWindow = w
	}
}

// armEdit prefills the input with the field's value: enter applies,
// esc cancels, a slash command disarms.
func (m *Model) armEdit(f SettingField) {
	cp := f
	m.saveTabPresentation() // Borrow the shared composer without losing its ordinary draft.
	m.Settings.Editing = &cp
	m.Input.SetValue(m.settingValue(f))
	m.Input.SetCursor(len(m.Input.Value()))
	// The Settings composer already carries edit guidance. A global alert
	// would incorrectly advertise these controls in a different retained tab.
}

// applyEdit applies the typed value to the armed field and disarms:
// validation failures keep their error on screen, nothing written.
func (m *Model) applyEdit(input string) {
	f := m.Settings.Editing
	if f == nil {
		return
	}
	if m.applyField(*f, input) {
		m.disarmEdit()
	}
}

func (m *Model) disarmEdit() {
	m.Settings.Editing = nil
	m.settingsEditor = nil
	m.Input.Reset()
	m.restoreTabEditor()
}

// saveSettings persists the working config: config.Save validates
// the whole file first, so a bad value refuses with nothing written.
func (m *Model) saveSettings() {
	if m.Tests.Busy != "" {
		m.alert("info", "save configuration after the test command settles")
		return
	}
	if m.CfgPath == "" {
		m.alert("err", "no config file in use — relaunch with --config to persist")
		return
	}
	if m.Settings.Applying {
		m.alert("err", "decision activation is unsettled — wait before saving")
		return
	}
	if m.Settings.DecisionDraft != nil {
		if !m.Cfg.Decision.Disabled && m.decisionOwner() != nil {
			m.alert("err", "decision selection is staged — press a before saving")
			return
		}
		m.commitExternalDecision()
	}
	if err := config.SaveSettings(m.CfgPath, m.Cfg); err != nil {
		m.alert("err", err.Error())
		return
	}
	m.syncSetupAfterSave()
	notice := "Saved " + m.settingsScope() + "; project check stays in this workspace"
	if m.Settings.DecisionNotice != "" {
		notice += "; " + m.Settings.DecisionNotice
	}
	m.alert("info", notice)
}

// settingsKey drives the form: arrows move, enter edits or cycles,
// s saves.
func (m Model) settingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	fields := settingFields()
	switch msg.Type {
	case tea.KeyUp:
		m.Settings.Sel = (m.Settings.Sel - 1 + len(fields)) % len(fields)
		return m, nil, true
	case tea.KeyDown:
		m.Settings.Sel = (m.Settings.Sel + 1) % len(fields)
		return m, nil, true
	case tea.KeyLeft, tea.KeyRight:
		if f := &fields[m.Settings.Sel]; f.Kind == "bool" || f.Kind == "auth" || f.Kind == "device" || f.Kind == "submode" || f.Kind == "runtime" || f.Kind == "protocol" {
			m.cycleField(*f)
			return m, nil, true
		}
		return m, nil, false
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		f := fields[m.Settings.Sel]
		if f.Kind == "bool" || f.Kind == "auth" || f.Kind == "device" || f.Kind == "submode" || f.Kind == "runtime" || f.Kind == "protocol" {
			m.cycleField(f)
		} else {
			m.armEdit(f)
		}
		return m, nil, true
	}
	if msg.String() == "a" && strings.TrimSpace(m.Input.Value()) == "" {
		cmd := m.applyDecisionSelection()
		return m, cmd, true
	}
	if msg.String() == "s" && strings.TrimSpace(m.Input.Value()) == "" {
		m.saveSettings()
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) settingsScope() string {
	if !m.Cfg.ProjectOverride && config.ManagedWorkspaceConfig(m.CfgPath) {
		path, _ := config.PreferencesPath()
		return "personal defaults for all projects · " + path
	}
	return "explicit project overrides · " + m.CfgPath
}

func (m *Model) renderSettings(w, vh int) string {
	fields := settingFields()
	sel := min(max(0, m.Settings.Sel), len(fields)-1)
	current := fields[sel]
	pane := surface{Title: "Settings · " + current.Section, Selected: sel, PinDetails: true,
		Status: []string{"worker edits live; decision selection staged · a apply · s save", "decision disabled changes future-run availability immediately", m.settingsScope()},
		Hints:  []string{m.ownedHint("↑↓ move · enter edit · ←/→ cycle · a apply decision · s save · " + m.escapeHint())}}
	if m.CfgPath == "" {
		pane.Status = append(pane.Status, "(no file: s refuses)")
	}
	for _, field := range fields {
		value := m.settingValue(field)
		if m.Settings.Editing != nil && *m.Settings.Editing == field {
			value += " ◀ editing"
		}
		pane.Rows = append(pane.Rows, surfaceRow{field.Label, value})
	}
	rule := "Working configuration · future requests · s saves"
	if current.Section == "params" {
		rule = "Requested control · runtime effect shown here"
	}
	if current.Section == "decision" {
		rule = "Decision configuration · a applies · s saves"
		if m.Settings.DecisionDraft != nil {
			rule = "Staged decision selection · a applies · s saves configuration"
		}
		if m.Settings.Applying {
			rule = "Applying decision selection · awaiting activation receipt"
		}
	}
	pane.Details = []string{rule, current.Label + ": " + m.settingValue(current)}
	if m.Settings.DecisionNotice != "" {
		pane.Details = append(pane.Details, m.Settings.DecisionNotice)
	}
	for _, note := range strings.Split(llm.RequestedControlNotice(m.Cfg.Model.Runtime, run.ModelParams(m.Cfg.Params)), "; ") {
		if strings.HasPrefix(note, current.Key) || vh > len(fields)+12 {
			pane.Details = append(pane.Details, note)
		}
	}
	return pane.view(w, vh, 0, true)
}
