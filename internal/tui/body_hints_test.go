package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestR13BodyHintsActualEscape(t *testing.T) {
	for _, id := range []ViewID{ViewPlans, ViewDiff, ViewSessions, ViewFiles, ViewDecisions, ViewWorkflows, ViewStatus} {
		t.Run(string(id), func(t *testing.T) {
			m := New(testCfg(), "test")
			m.Width, m.Height = 160, 40
			m.navigate(id, "") // Loading commands are deliberately not executed.
			m.Input.SetValue("")
			m.alert("warn", "owned notice")
			before := ansi.Strip(m.View())
			if !strings.Contains(before, "esc dismisses alert") || strings.Contains(before, "esc chat") || strings.Contains(before, "esc back") {
				t.Fatalf("contradictory alert hint:\n%s", before)
			}
			m = key(m, "esc")
			if m.Tabs[m.Active].at.view != id || !m.alertHidden {
				t.Fatal("first Escape did not only dismiss the alert")
			}
			if !strings.Contains(ansi.Strip(m.View()), "esc chat") {
				t.Fatal("clear view does not advertise return to chat")
			}
			m = key(m, "esc")
			if m.Tabs[m.Active].at.view != ViewChat {
				t.Fatal("second Escape did not return to chat")
			}
		})
	}
}

func TestR13FilesErrorEscapeHint(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width, m.Height = 120, 40
	m.navigate(ViewFiles, "")
	m.Files.Root, m.Files.Path = "owned-root", "missing.txt"
	m.onFilesOpened(filesOpenedMsg{root: m.Files.Root, title: m.Files.Path, ticket: m.Files.Ticket, err: errors.New("owned missing cause")})
	m.alert("warn", "owned notice")
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "owned missing cause") || !strings.Contains(v, "retrying each refresh") || strings.Contains(v, "esc back") || !strings.Contains(v, "esc closes detail") {
		t.Fatalf("missing-file hint:\n%s", v)
	}
	m = key(m, "esc")
	if m.Files.Path != "" || m.Files.Err != "" || len(m.Alerts) != 1 || !m.filesActive() {
		t.Fatal("Escape should close unavailable detail before alert")
	}
	if !strings.Contains(ansi.Strip(m.View()), "esc dismisses alert") {
		t.Fatal("alert hint after detail close")
	}
}
