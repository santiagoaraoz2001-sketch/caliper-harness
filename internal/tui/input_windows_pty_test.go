//go:build windows

package tui

import (
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The standalone driver reconstructs this actual styled source frame after
// Windows ConPTY. No runtime, graph, model or game is started.
func TestConsoleCRLFRenderPTYHelper(t *testing.T) {
	if os.Getenv("CALIPER_RENDER_PROBE") == "" {
		t.Skip("requires real ConPTY driver")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	lines := []string{"# Cargo examples\r", "ordinary source\r", "// café 日本語\r"}
	styled := make([]string, len(lines))
	for i, line := range lines {
		styled[i] = sourceText(line)
	}
	fmt.Print("\x1b[2J\x1b[H" + Box("CRLF captured source", strings.Join(Numbered(styled, 116), "\n"), 120) + "\r\nRENDER_END\r\n")
}

// Invoked only by scripts/test-windows-input.py inside a real ConPTY. It uses
// the product console adapter and composer, but never starts an inference run.
func TestConsoleInputPTYHelper(t *testing.T) {
	path := os.Getenv("CALIPER_INPUT_PROBE")
	if path == "" {
		t.Skip("requires real ConPTY driver")
	}
	opts, closeInput, err := ConsoleInput()
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	m := New(testCfg(), "test")
	m.ModelName = ""
	p := tea.NewProgram(consoleProbe{model: m, path: path}, append(opts, tea.WithAltScreen())...)
	stopConsoleSize := ObserveConsoleSize(p)
	defer stopConsoleSize()
	final, err := p.Run()
	if err != nil {
		t.Fatal(err)
	}
	if final.(consoleProbe).writes != 1 {
		t.Fatal("probe was not completed")
	}
}

type consoleProbe struct {
	model  Model
	path   string
	keys   []tea.KeyMsg
	writes int
	sizes  []tea.WindowSizeMsg
}

func (m consoleProbe) Init() tea.Cmd { return nil }
func (m consoleProbe) View() string  { return "CALIPER_INPUT_READY\n" }
func (m consoleProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.sizes = append(m.sizes, size)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.Type == tea.KeyCtrlC {
			raw, _ := json.Marshal(map[string]any{"history": m.model.Hist, "draft": m.model.Input.Value(), "keys": m.keys, "sizes": m.sizes})
			if os.WriteFile(m.path, raw, 0600) == nil {
				m.writes++
			}
			return m, tea.Quit
		}
		m.keys = append(m.keys, k)
	}
	updated, _ := m.model.Update(msg)
	m.model = updated.(Model)
	return m, nil
}
