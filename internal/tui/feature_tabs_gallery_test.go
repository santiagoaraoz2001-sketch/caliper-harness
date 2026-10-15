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

func TestTUIFeatureTabsGallery(t *testing.T) {
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
		m := journeyFixture(sz[0], sz[1])
		m.St.Model, m.ModelName = "Local model", "Local model"
		for _, v := range []ViewID{ViewSettings, ViewStatus, ViewFiles, ViewPlans, ViewSessions, ViewDecisions, ViewWorkflows} {
			m.navigate(v, "")
		}
		m = submit(m, "/status")
		capture("retained-status-tabs", m)
		m = submit(m, "/plans")
		capture("retained-plans-tabs", m)
		m = submit(m, "/settings")
		m.Settings.Sel = 10
		capture("retained-settings-tabs", m)
		m = submit(m, "/files")
		m.Files.Path = "internal/runtime/checks.go"
		m.Files.Viewer = &FileView{Title: m.Files.Path, Logical: []string{"package runtime", "", "func ready() bool {", "    return runtime.Ready()", "}"}}
		capture("retained-file-viewer-tabs", m)
		m = submit(m, "/chat")
		capture("retained-chat-tabs", m)
		m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: rowTask}})
		capture("retained-workflow-tabs", m)
		m = submit(m, "/chat")
		m.Chat.Running, m.Chat.Streaming = true, true
		m = submit(m, "/settings")
		capture("retained-tabs-during-model-work", m)
		m.Chat.Running, m.Chat.Streaming = false, false
		m.Settings.Sel = 1
		m.armEdit(settingFields()[1])
		m.Input.SetValue("Retained unfinished edit")
		m.switchTab(m.chatTabIndex())
		capture("dormant-settings-edit-chat", m)
		m.switchTab(featureIndex(m, ViewSettings))
		capture("retained-settings-edit", m)
		m = viewportKey(m, tea.KeyEsc)
	}
}
