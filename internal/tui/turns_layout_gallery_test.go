package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"os"
	"path/filepath"
	"testing"
)

func TestTUITurnsLayoutGallery(t *testing.T) {
	dir := os.Getenv("TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("opt-in actual renderer")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		capture := func(name string, m Model) {
			m.resizeComposer()
			viewportFrame(t, m)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.ansi", name, sz[0])), []byte(m.View()), 0644); err != nil {
				t.Fatal(err)
			}
		}
		m := turnsFixture(sz[0], sz[1])
		for i, line := range m.Chat.Lines {
			if line.Kind == lineUser {
				m.Chat.Lines[i].Text = "Explain the startup checks and their failure states."
			}
			if line.Kind == lineAssistant {
				m.Chat.Lines[i].Text = "The checks have three distinct states: pending, confirmed and failed.\n\n**What to preserve**\n- Keep the actual command and its result together.\n- Show failure details before secondary metadata.\n- Keep the header and composer visible while browsing.\n\n**What needs inspection**\nThe recorded output is an observation. Qualification remains separate.\n\nI will inspect the existing owning code and retain all current controls.\n\nThe proposed layout uses the current theme, spacing and source viewer.\n\nLong output remains available in the chat scrollback."
			}
		}
		m.St.Model, m.ModelName = "Local model", "Local model"
		m = submit(m, "/turns -5")
		capture("turns-five-earlier", m)
		m = submit(m, "/turns current")
		capture("turns-latest", m)
		m.Input.SetValue("/tur")
		capture("turns-command", m)
		m.Input.SetValue("")
		m = submit(m, "/turns -99")
		capture("turns-unavailable", m)
		m = journeyFixture(sz[0], sz[1])
		m.navigate(ViewSettings, "")
		for i, f := range settingFields() {
			if f.Key == "max_output_tokens" {
				m.Settings.Sel = i
			}
		}
		capture("adaptive-settings", m)
		m = journeyFixture(sz[0], sz[1])
		m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: rowTask}})
		capture("compact-workflow-task", m)
		m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: "brief"}})
		capture("workflow-full-ancestry", m)
		m = journeyFixture(sz[0], sz[1])
		m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: "attempt", Attempt: 1}})
		m = viewportKey(m, tea.KeyDown)
		m = viewportKey(m, tea.KeyEnter)
		capture("compact-workflow-receipt", m)
		m = historyFixture(sz[0], sz[1])
		m = submit(m, "/changes space name.go")
		capture("filtered-changes", m)
		for i := range m.Diff.Events {
			m.Diff.Events[i].Truncated = true
		}
		capture("filtered-changes-partial", m)
		m.navigate(ViewChanges, "")
		m.syncDiffScope()
		capture("unfiltered-changes", m)
	}
}
