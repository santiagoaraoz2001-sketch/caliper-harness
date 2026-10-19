package tui

import (
	"bytes"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/tui/thirdparty/textarea"
	"strings"
)

type SetupState struct {
	Stage        int
	Judge        config.DecisionConfig
	JudgePick    string
	Serve        config.ServeConfig
	Add          *connectionForm
	Added        []setupChoice
	Scroll       int
	CheckFocused bool
	Pick         string
	Model        config.ModelConfig
	Check        textarea.Model
	Started      bool
	Original     []byte
	Existed      bool
	Path, Err    string
}
type setupChoice struct {
	key, label, state string
	model             config.ModelConfig
	judge             *config.DecisionConfig
}

func (m Model) setupActive() bool { return m.Tabs[m.Active].at.view == ViewSetup }
func (m Model) setupChoices() []setupChoice {
	out := append([]setupChoice(nil), m.Setup.Added...)
	seen := map[string]bool{}
	for _, r := range m.Serve.Rows {
		if r.Sidecar || !r.IsModel || r.Ep.Model == "" {
			continue
		}
		if r.Asset != nil {
			if !r.Asset.LoadCandidate() {
				continue
			}
		} else if !r.Have || !r.Health.Up {
			continue
		}
		candidate := m.Cfg.Model
		candidate.Name = r.Ep.Model
		candidate.Server = r.Health.APIBase
		if candidate.Server == "" {
			candidate.Server = r.Ep.URL
		}
		candidate.Runtime = r.Health.Runtime
		if r.Health.Kind == "ollama" || r.Ep.Kind == "ollama" {
			candidate.Runtime = "ollama"
		}
		if r.Health.Kind == "rulith" || r.Ep.Kind == "rulith" {
			candidate.Runtime = "rulith"
		}
		if r.Asset != nil {
			candidate.Runtime = r.Asset.Runtime
		}
		if candidate.Runtime == "" && r.Ours && r.Recipe != nil {
			candidate.Runtime = r.Recipe.Runtime
		}
		if candidate.Runtime == "" && r.Ep.URL == m.Cfg.Model.Server {
			candidate.Runtime = m.Cfg.Model.Runtime
		}
		if candidate.Runtime == "" {
			candidate.Runtime = "openai"
		}
		key := candidate.Server + "\x00" + candidate.Name + "\x00" + candidate.Runtime
		if seen[key] {
			continue
		}
		seen[key] = true
		state := "endpoint found"
		if r.Asset != nil {
			state = "installed · load in Status"
		}
		out = append(out, setupChoice{key: key, label: candidate.Name, state: state, model: candidate})
	}
	return out
}
func (m Model) setupBusy() bool {
	return m.Chat.Running || m.Chat.Dialog != nil || m.modelWorkActive() || m.Wf.Busy != "" || m.Serve.Loading != "" || m.Settings.Applying || m.Settings.DecisionDraft != nil
}

// OpenSetup only stages presentation; Save commits through the config owner.
func (m *Model) OpenSetup() {
	if m.setupBusy() {
		m.alert("info", "wait for active work to settle before setting up this project")
		return
	}
	if !m.Setup.Started {
		path := m.CfgPath
		if path == "" {
			path = config.DefaultFile()
			m.CfgPath = path
		}
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			m.alert("err", fmt.Sprintf("cannot inspect config %s: %v", path, err))
			return
		}
		field := newComposer()
		field.SetHeight(2)
		field.SetWidth(max(1, m.Width-8))
		field.Placeholder = "Your project's check command"
		field.SetValue(m.Cfg.Coding.CheckCommand)
		m.Setup = SetupState{Started: true, Path: path, Original: raw, Existed: err == nil, Model: m.Cfg.Model, Judge: m.Cfg.Decision, Serve: m.Cfg.Serve, Check: field}
	}
	m.navigate(ViewSetup, "")
}
func (m Model) setupIndex(choices []setupChoice) int {
	for i, c := range choices {
		if c.key == m.setupPick() {
			return i
		}
	}
	if m.setupPick() == "" && len(choices) > 0 {
		return 0
	}
	return -1
}
func (m *Model) setupSave() {
	if m.setupBusy() {
		m.Setup.Err = "Active work must settle before saving configuration"
		return
	}
	found := false
	for _, c := range m.setupChoices() {
		found = found || c.key == m.Setup.Pick
	}
	if !found {
		m.Setup.Err = "Selected model is no longer available · return to Model and rescan"
		return
	}
	raw, err := os.ReadFile(m.Setup.Path)
	if err != nil && !os.IsNotExist(err) {
		m.Setup.Err = err.Error()
		return
	}
	if (err == nil) != m.Setup.Existed || !bytes.Equal(raw, m.Setup.Original) {
		m.Setup.Err = "Configuration changed outside Setup · restart Caliper to reload it"
		return
	}
	candidate := m.Cfg
	candidate.Model.Name = m.Setup.Model.Name
	candidate.Model.Server = m.Setup.Model.Server
	candidate.Model.Runtime = m.Setup.Model.Runtime
	candidate.Decision = m.Setup.Judge
	candidate.Serve = m.Setup.Serve
	candidate.Coding.CheckCommand = strings.TrimSpace(m.Setup.Check.Value())
	saved, err := config.ProjectBytes(candidate)
	if err != nil {
		m.Setup.Err = err.Error()
		return
	}
	if err := candidate.Validate(); err != nil {
		m.Setup.Err = err.Error()
		return
	}
	if err := os.WriteFile(m.Setup.Path, saved, 0644); err != nil {
		m.Setup.Err = err.Error()
		return
	}
	if err := config.SaveConnections(candidate); err != nil {
		m.Setup.Original, m.Setup.Existed = saved, true
		m.Setup.Err = "Project saved, but reusable connections failed: " + err.Error()
		return
	}
	m.Cfg = candidate
	m.CfgPath = m.Setup.Path
	m.syncDerived()
	// The feature tab survives Save. Keep its editor and the exact bytes written
	// by the config owner so later tab navigation remains usable and guarded.
	m.Setup.Stage, m.Setup.Scroll = 0, 0
	m.Setup.Original, m.Setup.Existed = saved, true
	m.Setup.Model, m.Setup.Err = candidate.Model, ""
	m.focusChat()
	m.alert("info", "Connections saved for all projects · inference unchecked · /status to load or inspect")
}
func (m Model) setupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.setupActive() || m.Chat.Dialog != nil || m.Input.Value() != "" {
		return m, nil, false
	}
	if m.Setup.Add != nil {
		return m.connectionKey(msg)
	}
	if m.Setup.Stage == 2 {
		if msg.Type == tea.KeyTab {
			m.Setup.CheckFocused = !m.Setup.CheckFocused
			return m, nil, true
		}
		if !m.Setup.CheckFocused {
			return m, nil, false
		}
		if msg.Type == tea.KeyEnter {

			m.Setup.Stage = 3
			m.Setup.Err = ""
			return m, nil, true
		}
		var cmd tea.Cmd
		m.Setup.Check, cmd = m.Setup.Check.Update(msg)
		return m, cmd, true
	}
	choices := m.currentSetupChoices()
	i := m.setupIndex(choices)
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown:
		if m.Setup.Stage <= 1 && len(choices) > 0 {
			if msg.Type == tea.KeyDown {
				i = min(len(choices)-1, i+1)
			} else {
				i = max(0, i-1)
			}
			if m.Setup.Stage == 1 {
				m.Setup.JudgePick = choices[i].key
			} else {
				m.Setup.Pick = choices[i].key
			}
			m.Setup.Err = ""
		}
		return m, nil, true
	case tea.KeyEnter:
		if m.Setup.Stage == 3 {
			m.setupSave()
			return m, nil, true
		}
		if i < 0 {
			m.Setup.Err = "No available model · r rescans · /settings configures another endpoint"
			return m, nil, true
		}
		if m.Setup.Stage == 0 {
			m.Setup.Pick, m.Setup.Model = choices[i].key, choices[i].model
			m.Setup.Stage = 1
		} else {
			m.Setup.JudgePick = choices[i].key
			if choices[i].judge != nil {
				m.Setup.Judge = *choices[i].judge
			} else {
				m.Setup.Judge.Server = choices[i].model.Server
				m.Setup.Judge.Model = choices[i].model.Name
				m.Setup.Judge.Runtime = choices[i].model.Runtime
				m.Setup.Judge.Path = ""
				m.Setup.Judge.Revision = ""
				m.Setup.Judge.Device = "auto"
			}
			m.Setup.Stage = 2
			m.Setup.CheckFocused = true
		}
		m.Setup.Err = ""
		return m, nil, true
	case tea.KeyRunes:
		if msg.String() == "a" && m.Setup.Stage < 2 {
			m.openConnectionForm()
			return m, nil, true
		}
		if msg.String() == "r" {
			return m, m.probeServe(), true
		}
	case tea.KeyPgUp, tea.KeyPgDown:
		if m.Setup.Stage == 3 {
			if msg.Type == tea.KeyPgUp {
				m.Setup.Scroll = max(0, m.Setup.Scroll-1)
			} else {
				m.Setup.Scroll++
			}
			return m, nil, true
		}
	}
	return m, nil, false
}

func (s SetupState) reviewRows(width int) []string {
	var rows []string
	for _, kv := range [][2]string{{"Model", s.Model.Name}, {"Runtime", s.Model.Runtime}, {"Endpoint", s.Model.Server}, {"Judge", s.Judge.Model + " · " + s.Judge.Runtime}, {"Judge endpoint", s.Judge.Server}, {"Project check (optional)", s.Check.Value()}, {"Save to", s.Path}} {
		for _, line := range Wrap(kv[0]+": "+kv[1], width) {
			rows = append(rows, Body.Render(line))
		}
	}
	return append(rows, Guidance.Render("Model loading and inference remain unchecked."))
}

func (m Model) renderSetup(w, h int) string {
	inner := BoxInner(w)
	s := m.Setup
	if s.Add != nil {
		return m.renderConnection(w, h)
	}
	titles := []string{"Worker", "Judge", "Project check", "Review"}
	body := []string{}
	if h > 6 {
		body = append(body, Title.Render(titles[s.Stage]))
	}
	hint := "↑↓ select · enter continue · a add server/model · r rescan · esc back"
	switch s.Stage {
	case 0, 1:
		if s.Stage == 1 && h > 6 {
			body = append(body, Guidance.Render("Decision-trained models recommended. Chat-model confidence is self-reported."))
		}
		choices := m.currentSetupChoices()
		selected := m.setupIndex(choices)
		var rows []string
		for i, c := range choices {
			line := "  " + c.label
			if i == selected {
				line = "› " + c.label
				rows = append(rows, selectionRow(truncate(line, inner), inner))
			} else {
				rows = append(rows, Body.Render(truncate(line, inner)))
			}
		}
		if len(rows) == 0 {
			body = append(body, Guidance.Render("No models found · a add endpoint or checkpoint · r rescan"))
		} else {
			body = append(body, listWindow(rows, selected, max(1, h-8))...)
			if selected >= 0 {
				body = append(body, Guidance.Render(truncate(choices[selected].state, inner)))
			}
		}
	case 2:
		if h > 6 {
			body = append(body, Guidance.Render("Optional project check; kept only in this project."))
		}
		field := s.Check
		field.SetWidth(max(1, inner-2))
		field.SetHeight(min(2, max(1, h-7)))
		if !s.CheckFocused || m.Chat.Dialog != nil {
			field.Blur()
		}
		body = append(body, strings.Split(field.View(), "\n")...)
		hint = "enter review · tab field/command · ctrl+j newline · esc judge"
	case 3:
		review := s.reviewRows(inner)
		room := max(1, h-6)
		start := min(s.Scroll, max(0, len(review)-room))
		body = append(body, review[start:min(len(review), start+room)]...)
		hint = "enter save · pgup/pgdn review · esc project check"
	}
	if s.Err != "" {
		body = append(body, ErrSt.Render(truncate(s.Err, inner)))
	}
	return panel("Setup · "+filepath.Base(s.Path), body, []string{m.ownedHint(hint)}, w, h)
}

func (m Model) setupPick() string {
	if m.Setup.Stage == 1 {
		return m.Setup.JudgePick
	}
	return m.Setup.Pick
}
func (m Model) currentSetupChoices() []setupChoice {
	out := m.setupChoices()
	if m.Setup.Stage != 1 {
		return out
	}
	// Keep the configured native selection available even before its sidecar is running.
	d := m.Setup.Judge
	if d.Runtime == "laya" || d.Runtime == "system_one" {
		out = append([]setupChoice{{key: "native:" + d.Server + ":" + d.Model, label: d.Model, state: "native decision service · connection untested", judge: &d}}, out...)
	}
	for _, r := range m.Serve.Rows {
		if r.Sidecar && r.Asset != nil && r.Asset.Complete {
			d := m.Setup.Judge
			d.Model = r.Asset.ID
			d.Path = r.Asset.Path
			d.Revision = r.Asset.Revision
			d.Runtime = r.Asset.Runtime
			out = append(out, setupChoice{key: "native:" + d.Path, label: d.Model, state: r.Asset.Reason, judge: &d})
		}
	}
	return out
}
