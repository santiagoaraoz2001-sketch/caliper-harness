package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Blueprint interface/src/ui/tokens.css: accent, ok, warning, error, text-3.
const activityBlue = "#6CB6FF"
const activityAmber = "#F2B84B"
const activityRed = "#FF7884"
const activityUnknown = "#9AA4A8"

type workerObservation struct{ Server, Model, State string }

func (m Model) modelWorkActive() bool {
	if m.Chat.Running || m.Chat.Compacting || m.Wf.Busy != "" || m.Tests.Busy != "" {
		return true
	}
	for i, t := range m.Tabs {
		if i != m.chatTabIndex() && t.feature == "" && t.chat.Compacting {
			return true
		}
	}
	return false
}

type activityTickMsg struct{}

func (m Model) activityAnimating() bool {
	return !m.ReducedMotion && m.Chat.Dialog == nil && m.modelWorkActive()
}
func (m Model) activityState() (label, color string) {
	if m.Chat.Dialog != nil {
		return "Waiting for you", activityAmber
	}
	if m.modelWorkActive() {
		if (m.Chat.Running && m.Chat.stopFlag) || m.Tests.Stopping {
			return "Stopping", activityAmber
		}
		return "Running", activityBlue
	}
	if o := m.WorkerObservation; o.Server == m.Cfg.Model.Server && o.Model == m.Cfg.Model.Name {
		switch o.State {
		case "online":
			return "Online · last check", Accent
		case "ready":
			return "Ready · last reply", Accent
		case "issue":
			return "Issue · last observation", activityAmber
		case "offline":
			return "Offline · last check", activityRed
		}
	}
	return "Unchecked", activityUnknown
}
func (m Model) activityLabel() string {
	label, color := m.activityState()
	glyph := "●"
	if m.activityAnimating() {
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		glyph = string(frames[m.ActivityFrame%uint64(len(frames))])
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph + " " + label)
}
func (m *Model) observeWorker(state string) {
	m.WorkerObservation = workerObservation{Server: m.Server, Model: m.ModelName, State: state}
}
