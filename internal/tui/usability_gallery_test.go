package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"specific-harness/internal/watch"
)

func TestTUIUsabilityGallery(t *testing.T) {
	dir := os.Getenv("TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("opt-in actual renderer")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		capture := func(name string, m Model) {
			m.resizeComposer()
			viewportFrame(t, m)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.ansi", name, size[0])), []byte(m.View()), 0644); err != nil {
				t.Fatal(err)
			}
		}
		m := journeyFixture(size[0], size[1])
		m.St.Model, m.ModelName = "Local model", "Local model"
		m.Input.SetValue("A multiline request\nKeep current commands.\nPreserve the working directory.")
		capture("single-composer-prompt", m)
		m.Input.SetValue(strings.Repeat("retained line\n", 8) + "current line")
		m = viewportUpdate(m, tea.KeyMsg{Type: tea.KeyEnd})
		capture("scrolled-composer-prompt", m)
		m.Input.SetValue("")
		m.resizeComposer()
		m.Chat.Running = true
		for i := 0; i < 2000; i++ {
			m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: i, Path: fmt.Sprintf("artifact-%d.txt", i), Op: watch.OpModify, BeforeKnown: true, Added: []string{"latest retained line"}, AddedN: 1, At: time.Now()}})
		}
		m.Chat.Running = false
		capture("active-diff-burst", m)
		m.clearChat()
		for i := 0; i < 2000; i++ {
			m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: i + 2000, Path: fmt.Sprintf("artifact-%d.txt", i), Op: watch.OpModify, BeforeKnown: true, Added: []string{"latest retained line"}, AddedN: 1, At: time.Now()}})
		}
		capture("idle-diff-burst", m)
		m.clearChat()
		m.Chat.appendLine(lineUser, "Read the agent's messages without losing the input box.")
		m.Chat.appendLine(lineAssistant, strings.Join([]string{"I am checking the existing source and recorded results.", strings.Repeat("A retained observation stays readable in scrollback.\n", 30), "This is example data for UI review."}, "\n"))
		capture("scrollable-chat-live", m)
		m = viewportUpdate(m, tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 2, Button: tea.MouseButtonWheelUp})
		capture("scrollable-chat-reading", m)
		lipgloss.SetColorProfile(termenv.Ascii)
		capture("scrollable-chat-no-color", m)
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
}
