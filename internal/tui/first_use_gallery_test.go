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

func TestFirstUseGallery(t *testing.T) {
	dir := os.Getenv("TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("opt-in actual renderer")
	}
	_ = os.MkdirAll(dir, 0755)
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 12}} {
		capture := func(name, text string) {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.ansi", name, size[0])), []byte(text), 0644); err != nil {
				t.Fatal(err)
			}
		}
		capture("startup-identity", StartupHeader("0.0.1", "my-project", size[0]))
		m := setupFixture(t, size[0], size[1])
		m.Setup.Path = "caliper-harness.yaml"
		capture("setup-model", m.View())
		m = viewportKey(m, tea.KeyEnter)
		m = viewportKey(m, tea.KeyEnter)
		m.Setup.Check.SetValue("go test ./...")
		capture("setup-check", m.View())
		m = viewportKey(m, tea.KeyEnter)
		capture("setup-review", m.View())
		m.Setup.Err = "Cannot save configuration · permission denied"
		capture("setup-save-error", m.View())
		m.Setup.Stage = 0
		m.Serve.Rows = nil
		m.Setup.Err = ""
		capture("setup-empty", m.View())
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if err := os.WriteFile(filepath.Join(dir, "startup-monochrome-80.ansi"), []byte(StartupHeader("0.0.1", "my-project", 80)), 0644); err != nil {
		t.Fatal(err)
	}
}
