package tui

import (
	"bytes"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/tui/thirdparty/textarea"
	"strings"
)

type SetupState struct {
	Stage        int
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
}

func (m Model) setupActive() bool { return m.Tabs[m.Active].at.view == ViewSetup }
func (m Model) setupChoices() []setupChoice {
	var out []setupChoice
	seen := map[string]bool{}
	for _, r := range m.Serve.Rows {
		if r.Sidecar || !r.IsModel || r.Ep.Model == "" {
			continue
		}
		if r.Asset != nil {
			if r.Asset.Runtime != "mlx" || !r.Asset.Complete {
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
		out = append(out, setupChoice{key, candidate.Name, state, candidate})
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
		m.Setup = SetupState{Started: true, Path: path, Original: raw, Existed: err == nil, Model: m.Cfg.Model, Check: field}
	}
	m.navigate(ViewSetup, "")
}
func (m Model) setupIndex(choices []setupChoice) int {
	for i, c := range choices {
		if c.key == m.Setup.Pick {
			return i
		}
	}
	if m.Setup.Pick == "" && len(choices) > 0 {
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
	candidate.Coding.CheckCommand = strings.TrimSpace(m.Setup.Check.Value())
	if candidate.Coding.CheckCommand == "" {
		m.Setup.Err = "Enter the project check before saving"
		return
	}
	saved, err := yaml.Marshal(candidate)
	if err != nil {
		m.Setup.Err = err.Error()
		return
	}
	if err := config.Save(m.Setup.Path, candidate); err != nil {
		m.Setup.Err = err.Error()
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
	m.alert("info", "Configuration saved · inference unchecked · /status to inspect or load")
}
func (m Model) setupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.setupActive() || m.Chat.Dialog != nil || m.Input.Value() != "" {
		return m, nil, false
	}
	if m.Setup.Stage == 1 {
		if msg.Type == tea.KeyTab {
			m.Setup.CheckFocused = !m.Setup.CheckFocused
			return m, nil, true
		}
		if !m.Setup.CheckFocused {
			return m, nil, false
		}
		if msg.Type == tea.KeyEnter {
			if strings.TrimSpace(m.Setup.Check.Value()) == "" {
				m.Setup.Err = "Enter the exact command used to check this project"
				return m, nil, true
			}
			m.Setup.Stage = 2
			m.Setup.Err = ""
			return m, nil, true
		}
		var cmd tea.Cmd
		m.Setup.Check, cmd = m.Setup.Check.Update(msg)
		return m, cmd, true
	}
	choices := m.setupChoices()
	i := m.setupIndex(choices)
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown:
		if m.Setup.Stage == 0 && len(choices) > 0 {
			if msg.Type == tea.KeyDown {
				i = min(len(choices)-1, i+1)
			} else {
				i = max(0, i-1)
			}
			m.Setup.Pick = choices[i].key
			m.Setup.Err = ""
		}
		return m, nil, true
	case tea.KeyEnter:
		if m.Setup.Stage == 2 {
			m.setupSave()
			return m, nil, true
		}
		if i < 0 {
			m.Setup.Err = "No available model · r rescans · /settings configures another endpoint"
			return m, nil, true
		}
		m.Setup.Pick, m.Setup.Model = choices[i].key, choices[i].model
		m.Setup.Stage = 1
		m.Setup.CheckFocused = true
		m.Setup.Err = ""
		return m, nil, true
	case tea.KeyRunes:
		if msg.String() == "r" {
			return m, m.probeServe(), true
		}
	case tea.KeyPgUp, tea.KeyPgDown:
		if m.Setup.Stage == 2 {
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
	for _, kv := range [][2]string{{"Model", s.Model.Name}, {"Runtime", s.Model.Runtime}, {"Endpoint", s.Model.Server}, {"Project check", s.Check.Value()}, {"Save to", s.Path}} {
		for _, line := range Wrap(kv[0]+": "+kv[1], width) {
			rows = append(rows, Body.Render(line))
		}
	}
	return append(rows, Guidance.Render("Model loading and inference remain unchecked."))
}

func (m Model) renderSetup(w, h int) string {
	inner := BoxInner(w)
	s := m.Setup
	if h <= 6 {
		var compact []string
		if s.Err != "" {
			compact = append(compact, ErrSt.Render(truncate(s.Err, inner)))
		}
		switch s.Stage {
		case 0:
			choices := m.setupChoices()
			i := m.setupIndex(choices)
			label := "No model found · r rescan"
			if i >= 0 {
				label = "› " + choices[i].label
			}
			compact = append(compact, Body.Render(truncate(label, inner)))
		case 1:
			field := s.Check
			field.SetWidth(max(1, inner-2))
			field.SetHeight(1)
			if !s.CheckFocused || m.Chat.Dialog != nil {
				field.Blur()
			}
			compact = append(compact, strings.Split(field.View(), "\n")...)
		case 2:
			review := s.reviewRows(inner)
			room := max(1, h-2-len(compact))
			start := min(s.Scroll, max(0, len(review)-room))
			compact = append(compact, review[start:min(len(review), start+room)]...)
		}
		title := "Setup · " + []string{"Model", "Project check", "Review"}[s.Stage]
		if s.Stage == 2 {
			title = "Review · pgup/pgdn"
		}
		return panel(title, compact, nil, w, h)
	}
	steps := []string{"1 Model", "2 Project check", "3 Review"}
	for i := range steps {
		if i == s.Stage {
			steps[i] = InfoSt.Bold(true).Render(steps[i])
		} else {
			steps[i] = Guidance.Render(steps[i])
		}
	}
	body := []string{strings.Join(steps, Guidance.Render("  ›  ")), ""}
	hint := "↑↓ select · enter continue · r rescan · esc chat"
	switch s.Stage {
	case 0:
		body = append(body, Body.Render("Choose an existing model."), Guidance.Render("Selection stays staged until review and save."), "")
		choices := m.setupChoices()
		selected := m.setupIndex(choices)
		if len(choices) == 0 {
			body = append(body, WarnSt.Render("No model endpoint found yet."))
			for _, line := range Wrap("Start an existing Ollama or MLX server, then press r. For another endpoint, use /settings or /status. Caliper does not download model weights.", inner) {
				body = append(body, Guidance.Render(line))
			}
		} else {
			var rows []string
			for i, c := range choices {
				prefix := "  "
				if i == selected {
					prefix = "› "
				}
				row := prefix + truncate(c.label, max(1, inner-2))
				if i == selected {
					row = selectionRow(row, inner)
				} else {
					row = Body.Render(row)
				}
				rows = append(rows, row)
			}
			body = append(body, listWindow(rows, selected, max(1, h-len(body)-7))...)
			if selected >= 0 {
				body = append(body, Guidance.Render(choices[selected].state+" · "+choices[selected].model.Runtime), PathSt.Render(truncate(choices[selected].model.Server, inner)))
			}
		}
	case 1:
		body = append(body, Title.Render("How do you check this project?"))
		for _, line := range Wrap("Enter the exact command from this project's documentation. Setup saves it; it does not run it.", inner) {
			body = append(body, Guidance.Render(line))
		}
		body = append(body, "")
		field := s.Check
		if !s.CheckFocused || m.Chat.Dialog != nil {
			field.Blur()
		}
		field.SetWidth(max(1, inner-2))
		field.SetHeight(min(2, max(1, h-9)))
		body = append(body, strings.Split(field.View(), "\n")...)
		hint = "enter review · tab field/command · ctrl+j newline · esc model"
		if m.Chat.Dialog != nil {
			hint = "Setup paused while answering the question below"
		}
	case 2:
		review := s.reviewRows(inner)
		room := max(1, h-6)
		if s.Err != "" {
			room = max(1, room-len(Wrap(s.Err, inner)))
		}
		start := min(s.Scroll, max(0, len(review)-room))
		end := min(len(review), start+room)
		body = append(body, review[start:end]...)
		if len(review) > room {
			body = append(body, Guidance.Render(fmt.Sprintf("%d–%d/%d lines · pgup/pgdn", start+1, end, len(review))))
		}
		hint = "enter save configuration · esc project check"
	}
	if s.Err != "" {
		for _, line := range Wrap(s.Err, inner) {
			body = append(body, ErrSt.Render(line))
		}
	}
	if m.Input.Value() != "" {
		body = append(body, Guidance.Render("Your retained composer draft is active below."))
	}
	return panel("Setup · "+filepath.Base(s.Path), body, []string{m.ownedHint(hint)}, w, h)
}
