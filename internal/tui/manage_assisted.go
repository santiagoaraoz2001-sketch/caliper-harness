package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/config"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) helpBody() string {
	if m.HelpText != "" {
		return m.HelpText
	}
	return m.Reg.Help() + "\n" + keyHelp()
}

func (m *Model) openAssistedSetup() {
	path, err := config.ConnectionsPath()
	if err != nil {
		m.alert("err", "cannot locate reusable connections: "+err.Error())
		return
	}
	project := m.CfgPath
	if project == "" {
		project = config.DefaultFile()
	}
	project, err = filepath.Abs(project)
	if err != nil {
		m.alert("err", "cannot locate project settings: "+err.Error())
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		m.alert("err", "cannot locate current directory: "+err.Error())
		return
	}
	scope, command := "automatic workspace storage; personal defaults apply", "caliper config"
	if m.Cfg.ProjectOverride || !config.ManagedWorkspaceConfig(project) {
		scope = "project overrides personal defaults"
	}
	if m.Cfg.ProjectOverride {
		quoted := shellQuote(project)
		if runtime.GOOS == "windows" {
			quoted = "'" + strings.ReplaceAll(project, "'", "''") + "'"
		}
		command += " --config " + quoted
	}
	pc, err := m.Cfg.LoadPrompts()
	if err != nil {
		m.alert("err", "cannot load assisted setup prompt; check prompts.path: "+err.Error())
		return
	}
	text, err := pc.Render("setup.assisted", map[string]any{
		"OS": runtime.GOOS, "Connections": path, "Project": project,
		"Directory": directory, "Scope": scope, "ValidationCommand": command,
	})
	if err != nil {
		m.alert("err", "cannot render assisted setup; add setup.assisted from the shipped prompts.yaml to your selected catalog: "+err.Error())
		return
	}
	m.HelpText = text
	m.HelpOpen, m.HelpScroll, m.HelpNotice = true, 0, ""
}

type assistedCopiedMsg struct{ err error }

func copyAssistedPrompt(text string, write func(string) error) tea.Cmd {
	return func() tea.Msg { return assistedCopiedMsg{write(text)} }
}

func (m Model) copyAssistedSetup() tea.Cmd {
	return copyAssistedPrompt(m.HelpText, clipboard.WriteAll)
}

func (m Model) helpHeading() string {
	if m.HelpText != "" {
		return "LLM-assisted setup"
	}
	return "Commands & keys"
}

func (m Model) helpHint() string {
	if m.HelpText != "" {
		hint := "c copy full prompt · ↑↓ scroll · pgup/pgdn · home/end · esc close"
		if m.HelpNotice != "" {
			hint = fmt.Sprintf("%s · %s", m.HelpNotice, hint)
		}
		return hint
	}
	return "↑↓ scroll · pgup/pgdn page · home/end · esc/any other key closes"
}
