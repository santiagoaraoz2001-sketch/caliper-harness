package tui

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"runtime"
	"specific-harness/internal/config"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

//go:embed manage_assisted.md
var assistedSetupTemplate string

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
	m.HelpText = strings.NewReplacer("{{OS}}", runtime.GOOS, "{{CONNECTIONS}}", path, "{{PROJECT}}", project).Replace(assistedSetupTemplate)
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
