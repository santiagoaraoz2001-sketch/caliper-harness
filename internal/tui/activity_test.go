package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestActivityStateSourcesAndSpinnerLifecycle(t *testing.T) {
	m := New(testCfg(), "test")
	label, _ := m.activityState()
	if label != "Unchecked" {
		t.Fatal("configuration alone claims model readiness")
	}
	m.Chat.Running, m.Chat.Seq = true, 1
	next, cmd := m.Update(activityTickMsg{})
	m = next.(Model)
	if cmd != nil || m.ActivityFrame != 1 || !m.ActivityTickPending {
		t.Fatal("running work has no ticking indicator")
	}
	first := m.activityLabel()
	next, _ = m.Update(activityTickMsg{})
	m = next.(Model)
	if m.activityLabel() == first {
		t.Fatal("throbber did not advance")
	}
	m.Chat.Dialog = &Dialog{Kind: "auth", Prompt: "approve?"}
	next, cmd = m.Update(activityTickMsg{})
	m = next.(Model)
	if cmd != nil || m.ActivityTickPending {
		t.Fatal("indicator spins while waiting for user")
	}
	label, color := m.activityState()
	if label != "Waiting for you" || color != activityAmber {
		t.Fatal("waiting state wrong")
	}
	m.Chat.Dialog = nil
	m.onToken(tokenMsg{seq: 1, delta: "actual reply"})
	m.Chat.Running = false
	next, cmd = m.Update(activityTickMsg{})
	m = next.(Model)
	if cmd != nil || m.ActivityTickPending {
		t.Fatal("idle timer keeps running")
	}
	label, color = m.activityState()
	if label != "Ready · last reply" || color != Accent {
		t.Fatal("response did not establish readiness")
	}
	m.Cfg.Model.Name = "another model"
	label, _ = m.activityState()
	if label != "Unchecked" {
		t.Fatal("readiness leaked across model selection")
	}
	m.WorkerObservation = workerObservation{Server: m.Cfg.Model.Server, Model: m.Cfg.Model.Name, State: "offline"}
	label, color = m.activityState()
	if label != "Offline · last check" || color != activityRed {
		t.Fatal("offline state wrong")
	}
	m.Chat.Running = true
	m.ReducedMotion = true
	next, cmd = m.Update(activityTickMsg{})
	m = next.(Model)
	if cmd != nil || !strings.Contains(ansi.Strip(m.activityLabel()), "Running") {
		t.Fatal("reduced-motion status fails")
	}
}

func TestBackgroundCompactionKeepsActivityVisible(t *testing.T) {
	m := New(testCfg(), "test")
	m.Tabs[0].chat.Compacting = true
	m.newTab()
	// newTab saves the live chat before switching; establish the parked job as
	// delivered by the existing compaction owner, then inspect the active tab.
	m.Tabs[0].chat.Compacting = true
	if !m.activityAnimating() {
		t.Fatal("background compaction disappeared")
	}
	label, _ := m.activityState()
	if label != "Running" {
		t.Fatal("background model work appears idle")
	}
	m.Tabs[0].chat.Compacting = false
	if m.activityAnimating() {
		t.Fatal("completed compaction keeps spinning")
	}
}

func TestActivityHeaderReservesOneContentBuffer(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 12}, {34, 20}} {
		m := New(testCfg(), "test")
		m.Width, m.Height = size[0], size[1]
		width := m.Width
		m.Chat.Running = true
		m.Tabs[0].Title = strings.Repeat("long title ", 12)
		rows := m.tabChrome()
		buffer := 0
		if m.Width >= 64 && m.Height >= 20 {
			buffer = 1
		}
		if len(rows) != len(m.tabChromeRows())+buffer || (buffer == 1 && rows[len(rows)-1] != "") {
			t.Fatal("header must reserve one blank row at normal sizes without crowding compact content")
		}
		if !strings.Contains(ansi.Strip(rows[0]), "Running") {
			t.Fatal("running indicator missing from existing tab row")
		}
		for _, row := range rows {
			if ansi.StringWidth(row) > width {
				t.Fatal("header exceeds viewport")
			}
		}
	}
}
