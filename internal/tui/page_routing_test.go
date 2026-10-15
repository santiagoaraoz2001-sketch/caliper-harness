package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/session"
)

func TestPageKeysReachActiveViewer(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, view := range []ViewID{ViewFiles, ViewDiff, ViewPlans, ViewSessions} {
			t.Run(fmt.Sprintf("%dx%d/%v", size[0], size[1], view), func(t *testing.T) {
				m := fidelityModel(view)
				m.Width, m.Height = size[0], size[1]
				m.Input.SetValue("retained draft κ")
				m.Sessions.Detail = &SessionDetail{ID: "fixture"}
				for i := range 80 {
					m.Sessions.Detail.Events = append(m.Sessions.Detail.Events, session.Event{Kind: "result", Text: fmt.Sprintf("EVENT_%02d", i)})
				}
				scroll := func() int {
					if view == ViewSessions {
						return m.Sessions.Detail.Scroll
					}
					return *fidelityScroll(&m)
				}
				first := m.View()
				next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
				m = next.(Model)
				if cmd != nil || scroll() <= 0 || m.View() == first {
					t.Fatalf("PgDown did not reach active viewer: scroll=%d command=%v", scroll(), cmd != nil)
				}
				next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
				m = next.(Model)
				if cmd != nil || scroll() != 0 || m.View() != first || m.Input.Value() != "retained draft κ" {
					t.Fatal("PgUp did not restore initial frame and untouched draft")
				}
				if !strings.Contains(m.View(), "retained draft") {
					t.Fatal("composer draft disappeared")
				}
			})
		}
	}
}
