package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
	return []SettingField{
		{"model", "server", "model server", "text"},
		{"model", "name", "model name", "text"},
		{"model", "runtime", "model runtime", "runtime"},
		{"model", "tool_protocol", "tool protocol", "protocol"},
		{"coding", "check_command", "project check command (empty = recorded proposal)", "text"},
		{"agent", "max_run_seconds", "run seconds (0 = caller budget only)", "int"},
		{"params", "temperature", "temperature", "float"},
		{"params", "top_k", "top_k", "int"},
		{"params", "repetition_penalty", "repetition penalty", "float"},
		{"params", "thinking", "thinking requested", "bool"},
		{"params", "max_output_tokens", "output tokens including reasoning (0 = runtime policy unknown)", "int"},
		{"auth", "mode", "auth mode", "auth"},
		{"context", "window", "context window (0 = auto)", "int"},
		{"decision", "server", "decision server URL", "text"},
		{"decision", "runtime", "decision runtime", "runtime"},
		{"decision", "path", "decision assets path (optional)", "text"},
		{"decision", "revision", "decision revision (optional)", "text"},
		{"decision", "trust_remote_code", "trust decision remote code", "bool"},
		{"decision", "model", "decision model", "text"},
		{"decision", "device", "decision device", "device"},
		{"subagents", "mode", "subagent mode", "submode"},
		{"subagents", "max_subagents", "max subagents (0 = auto)", "int"},
		{"subagents", "context_window", "subagent window (0 = inherit)", "int"},
		{"workflows", "verify_followup", "review runs", "bool"},
		{"workflows", "max_retries", "max retries", "int"},
	}
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
	switch f.Section + "/" + f.Key {
	case "model/tool_protocol":
		if c.Model.ToolProtocol == "" {
			return "structured"
		}
		return c.Model.ToolProtocol
	case "coding/check_command":
		return c.Coding.CheckCommand
	case "agent/max_run_seconds":
		return strconv.Itoa(int(c.Agent.MaxRunSeconds))
	case "model/runtime":
		if c.Model.Runtime == "" {
			return llm.RuntimeOpenAI
		}
		return c.Model.Runtime
	case "model/server":
		return c.Model.Server
	case "model/name":
		return c.Model.Name
	case "params/temperature":
		return strconv.FormatFloat(c.Params.Temperature, 'f', -1, 64)
	case "params/top_k":
		return strconv.Itoa(c.Params.TopK)
	case "params/repetition_penalty":
		return strconv.FormatFloat(c.Params.RepetitionPenalty, 'f', -1, 64)
	case "params/thinking":
		return strconv.FormatBool(c.Params.Thinking)
	case "params/max_output_tokens":
		return strconv.Itoa(int(c.Params.MaxOutputTokens))
	case "auth/mode":
		return c.Auth.Mode
	case "context/window":
		return strconv.Itoa(c.Context.Window)
	case "decision/server":
		return c.Decision.Server
	case "decision/runtime":
		return c.Decision.Runtime
	case "decision/path":
		return c.Decision.Path
	case "decision/revision":
		return c.Decision.Revision
	case "decision/trust_remote_code":
		return strconv.FormatBool(c.Decision.TrustRemoteCode)
	case "decision/model":
		return c.Decision.Model
	case "decision/device":
		return c.Decision.Device
	case "subagents/mode":
		return c.Subagents.Mode
	case "subagents/max_subagents":
		return strconv.Itoa(c.Subagents.MaxSubagents)
	case "subagents/context_window":
		return strconv.Itoa(c.Subagents.ContextWindow)
	case "workflows/verify_followup":
		return strconv.FormatBool(c.Workflows.VerifyFollowup)
	case "workflows/max_retries":
		return strconv.Itoa(c.Workflows.MaxRetries)
	}
	return "?"
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
func (m *Model) applyField(f SettingField, val string) {
	next := m.Cfg
	if f.Section == "decision" && m.Settings.DecisionDraft != nil {
		next.Decision = *m.Settings.DecisionDraft
	}
	var err error
	setFloat := func(dst *float64) {
		var v float64
		v, err = strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err == nil {
			*dst = v
		}
	}
	setInt := func(dst *int) {
		var v int
		v, err = strconv.Atoi(strings.TrimSpace(val))
		if err == nil {
			*dst = v
		}
	}
	switch f.Section + "/" + f.Key {
	case "model/tool_protocol":
		next.Model.ToolProtocol = val
	case "coding/check_command":
		next.Coding.CheckCommand = val
	case "agent/max_run_seconds":
		var value int
		setInt(&value)
		next.Agent.MaxRunSeconds = config.RunSeconds(value)
	case "model/runtime":
		next.Model.Runtime = val
	case "model/server":
		next.Model.Server = strings.TrimSpace(val)
	case "model/name":
		next.Model.Name = strings.TrimSpace(val)
	case "params/temperature":
		setFloat(&next.Params.Temperature)
	case "params/top_k":
		setInt(&next.Params.TopK)
	case "params/repetition_penalty":
		setFloat(&next.Params.RepetitionPenalty)
	case "params/thinking":
		next.Params.Thinking = val == "true"
	case "params/max_output_tokens":
		var value int
		setInt(&value)
		next.Params.MaxOutputTokens = config.OutputTokens(value)
	case "auth/mode":
		next.Auth.Mode = val
	case "context/window":
		setInt(&next.Context.Window)
	case "decision/server":
		next.Decision.Server = strings.TrimSpace(val)
	case "decision/runtime":
		next.Decision.Runtime = val
	case "decision/path":
		next.Decision.Path = strings.TrimSpace(val)
	case "decision/revision":
		next.Decision.Revision = strings.TrimSpace(val)
	case "decision/trust_remote_code":
		next.Decision.TrustRemoteCode = val == "true"
	case "decision/model":
		next.Decision.Model = strings.TrimSpace(val)
	case "decision/device":
		next.Decision.Device = val
	case "subagents/mode":
		next.Subagents.Mode = val
	case "subagents/max_subagents":
		setInt(&next.Subagents.MaxSubagents)
	case "subagents/context_window":
		setInt(&next.Subagents.ContextWindow)
	case "workflows/verify_followup":
		next.Workflows.VerifyFollowup = val == "true"
	case "workflows/max_retries":
		setInt(&next.Workflows.MaxRetries)
	default:
		m.alert("err", fmt.Sprintf("unknown setting %s/%s", f.Section, f.Key))
		return
	}
	if err != nil {
		m.alert("err", fmt.Sprintf("%s.%s (%s) needs a number, got %q", f.Section, f.Key, f.Label, val))
		return
	}
	if verr := next.Validate(); verr != nil {
		m.alert("err", fmt.Sprintf("refusing %s=%q: %s", f.Label, val, verr.Error()))
		return
	}
	if f.Section == "decision" {
		m.Settings.DecisionDraft = &next.Decision
		m.Settings.DecisionSeq++
		m.Settings.DecisionNotice = "staged — a applies; external native services remain pending activation"
		m.alert("info", m.Settings.DecisionNotice)
		return
	}
	m.Cfg = next
	m.syncDerived()
	m.alert("info", fmt.Sprintf("%s live — s saves to file", f.Label))
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
	m.disarmEdit()
	if f == nil {
		return
	}
	m.applyField(*f, input)
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
	if m.CfgPath == "" {
		m.alert("err", "no config file in use — relaunch with --config to persist")
		return
	}
	if m.Settings.Applying {
		m.alert("err", "decision activation is unsettled — wait before saving")
		return
	}
	if m.Settings.DecisionDraft != nil {
		if m.decisionOwner() != nil {
			m.alert("err", "decision selection is staged — press a before saving")
			return
		}
		m.commitExternalDecision()
	}
	if err := m.persist(); err != nil {
		m.alert("err", err.Error())
		return
	}
	m.alert("info", fmt.Sprintf("saved %s; %s", m.CfgPath, m.Settings.DecisionNotice))
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

func (m Model) renderSettings(w, vh int) string {
	inner := BoxInner(w)
	fields := settingFields()
	labelWidth := settingLabelWidth(fields, inner)
	sel := min(max(0, m.Settings.Sel), len(fields)-1)
	if vh < 3 {
		selected := fields[sel].Label + ": " + m.settingValue(fields[sel])
		return strings.Join(fitRows([]string{truncate(selected, w)}, max(0, vh)), "\n")
	}
	hints := Wrap(m.ownedHint("↑↓ move · enter edit · ←/→ cycle · a apply decision · s save · "+m.escapeHint()), inner)
	body := []string{Muted.Render("worker edits live; decision edits staged · a apply · s save")}
	if m.CfgPath == "" {
		body = append(body, WarnSt.Bold(false).Render("(no file: s refuses)"))
	}
	body = append(body, SectionSt.Render(fields[sel].Section))
	var rows []string
	selectedLine := 0
	lastSection := ""
	for i, f := range fields {
		if f.Section != lastSection {
			lastSection = f.Section
			if f.Section != fields[sel].Section {
				rows = append(rows, SectionSt.Render(f.Section))
			}
		}
		value := m.settingValue(f)
		if m.Settings.Editing != nil && m.Settings.Editing.Key == f.Key && m.Settings.Editing.Section == f.Section {
			value += " ◀ editing"
		}
		label := truncate(f.Label, labelWidth)
		pad := max(2, labelWidth+2-ansi.StringWidth(label))
		line := "  " + Body.Render(label) + strings.Repeat(" ", pad) + InfoSt.Render(value)
		if i == sel {
			selectedLine = len(rows)
			selected := "❯ " + Body.Render(label) + strings.Repeat(" ", pad) + InfoSt.Render(value)
			rows = append(rows, shadedRow(selected, inner, SelectedSt.Bold(false), Shade))
		} else {
			rows = append(rows, truncate(line, inner))
		}
	}
	notes := []string{}
	details := Wrap(fields[sel].Label+": "+m.settingValue(fields[sel]), inner)
	for _, l := range details[:min(2, len(details))] {
		notes = append(notes, Body.Render(l))
	}
	if len(details) > 2 {
		notes = append(notes, Muted.Render("… enter edit shows the full value · esc cancels edit"))
	}
	for _, note := range strings.Split(llm.RequestedControlNotice(m.Cfg.Model.Runtime, run.ModelParams(m.Cfg.Params)), "; ") {
		if fields[sel].Section == "params" && strings.HasPrefix(note, fields[sel].Key) {
			for _, l := range Wrap(note, inner) {
				notes = append(notes, WarnSt.Bold(false).Render(l))
			}
		}
	}
	rule := "Working configuration · future requests · s saves"
	if fields[sel].Section == "params" {
		rule = "Requested control · runtime effect shown here"
	}
	if fields[sel].Section == "decision" {
		rule = "Decision configuration · a applies · s saves"
		if m.Settings.DecisionDraft != nil {
			rule = "Staged decision selection · a applies · s saves configuration"
		}
		if m.Settings.Applying {
			rule = "Applying decision selection · awaiting activation receipt"
		}
	}
	for _, l := range Wrap(rule, inner) {
		notes = append(notes, Guidance.Render(l))
	}
	if m.Settings.DecisionNotice != "" {
		for _, l := range Wrap(m.Settings.DecisionNotice, inner) {
			notes = append(notes, WarnSt.Bold(false).Render(l))
		}
	}
	// Unrelated runtime disclosures stay available when the entire table fits;
	// smaller panels explain only the selection instead of consuming its space.
	if vh-2-len(hints)-len(body) > len(rows)+len(notes)+4 {
		for _, note := range strings.Split(llm.RequestedControlNotice(m.Cfg.Model.Runtime, run.ModelParams(m.Cfg.Params)), "; ") {
			if fields[sel].Section == "params" && strings.HasPrefix(note, fields[sel].Key) {
				continue
			}
			for _, l := range Wrap(note, inner) {
				notes = append(notes, Guidance.Render(l))
			}
		}
	}
	notesBudget := min(len(notes), max(0, vh-2-len(hints)-len(body)-3))
	room := max(1, vh-2-len(hints)-len(body)-notesBudget)
	body = append(body, listWindow(rows, selectedLine, room)...)
	body = append(body, notes[:notesBudget]...)
	return panel("Settings · "+fields[sel].Section, body, hints, w, vh)
}

// Reserve a useful value column before expanding labels into spare cells.
func settingLabelWidth(fields []SettingField, inner int) int {
	longest := 0
	for _, f := range fields {
		longest = max(longest, ansi.StringWidth(f.Label))
	}
	valueWidth := min(36, max(12, inner/3))
	return max(1, min(longest, inner-4-valueWidth))
}
