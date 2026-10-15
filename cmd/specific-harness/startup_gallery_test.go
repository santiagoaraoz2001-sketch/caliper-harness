package main

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/tui"
	"testing"
)

// Capture the real startup renderer and checks in a disposable first-use scope.
// Native inference is deliberately outside this UI capture.
func TestStartupSequenceGallery(t *testing.T) {
	dir := os.Getenv("TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("opt-in actual startup renderer")
	}
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, width := range []int{80, 120} {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("startup-sequence-%d.ansi", width)))
		if err != nil {
			t.Fatal(err)
		}
		stderr := os.Stderr
		func() {
			os.Stderr = f
			defer func() { os.Stderr = stderr }()
			fmt.Fprint(f, tui.StartupHeader("0.0.1", "my-project", width))
			boot := &startupDisplay{width: width}
			boot.begin("Configuration", "Reading project settings")
			cfg, used, err := config.Load(filepath.Join(t.TempDir(), "caliper-harness.yaml"))
			if err != nil || used {
				t.Fatal("first-use fixture", err)
			}
			boot.finish("info", "First use · review settings in Setup")
			boot.begin("Instructions", "Validating the prompt catalog")
			if _, err := cfg.LoadPrompts(); err != nil {
				t.Fatal(err)
			}
			boot.finish("ok", "Prompt catalog validated")
			boot.dependencies()
			boot.line("info", "Model selection", "Choose an existing model in Setup")
			boot.line("ok", "Workspace", "Opening Caliper Harness")
		}()
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
