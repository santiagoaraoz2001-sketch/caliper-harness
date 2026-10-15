package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func inspectionUpdate(m Model, msg tea.Msg) Model { next, _ := m.Update(msg); return next.(Model) }
func inspectionFrame(t *testing.T, m Model) string {
	t.Helper()
	v := m.View()
	if lipgloss.Height(v) > m.Height {
		t.Fatalf("auth frame height %d exceeds %d", lipgloss.Height(v), m.Height)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > m.Width {
			t.Fatalf("auth row width %d exceeds %d", lipgloss.Width(l), m.Width)
		}
	}
	plain := ansi.Strip(v)
	if !m.HelpOpen && m.Chat.Dialog != nil && m.Chat.Dialog.Kind == "auth" {
		for _, hint := range []string{"Auth ·", "y approve", "n deny", "esc declines", "dialog open"} {
			if !strings.Contains(plain, hint) {
				t.Fatalf("unpinned approval hint %q", hint)
			}
		}
	}
	return plain
}

func TestAuthInspectionViewport(t *testing.T) {
	var rows []string
	for i := 0; i < 45; i++ {
		rows = append(rows, fmt.Sprintf("row-%02d: %s TAIL_%02d", i, strings.Repeat("κdata ", 25), i))
	}
	prompt := strings.Join(rows, "\n")
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := inspectionUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.Chat.Running = true
			m.Input.SetValue("draft unchanged")
			m.Chat.pinTop = 7
			m.Hist = []string{"past"}
			ch := make(chan string, 1)
			m = inspectionUpdate(m, showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: prompt, answer: ch})
			beforeHistory := append([]string{}, m.Hist...)
			beforeScroll := m.Chat.pinTop
			all := inspectionFrame(t, m)
			first := all
			m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
			if inspectionFrame(t, m) == first {
				t.Fatal("down did not scroll action")
			}
			m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyUp})
			if inspectionFrame(t, m) != first {
				t.Fatal("up did not restore action page")
			}
			for i := 0; i < 20; i++ {
				m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyPgDown})
				all += "\n" + inspectionFrame(t, m)
			}
			for _, line := range Wrap(prompt, BoxInner(m.Width)) {
				if !strings.Contains(all, line) {
					t.Fatalf("unreachable action line: %q", line)
				}
			}
			if m.Input.Value() != "draft unchanged" || !reflect.DeepEqual(m.Hist, beforeHistory) || m.Chat.pinTop != beforeScroll {
				t.Fatal("inspection changed draft/history/covered view")
			}
			select {
			case a := <-ch:
				t.Fatalf("inspection answered %q", a)
			default:
			}
			m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyHome})
			if inspectionFrame(t, m) != first {
				t.Fatal("home did not return first page")
			}
			m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyEnd})
			if !strings.Contains(inspectionFrame(t, m), "TAIL_44") {
				t.Fatal("end not reachable")
			}
			m = inspectionUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 40})
			if !strings.Contains(inspectionFrame(t, m), "TAIL_44") {
				t.Fatal("resize did not clamp final page")
			}
			m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
			if <-ch != "n" || m.Chat.Dialog != nil {
				t.Fatal("esc approval disposition changed")
			}
		})
	}
}

func TestAuthInspectionDraftStopAndHelp(t *testing.T) {
	m := inspectionUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Chat.Running = true
	cancelled := false
	m.Chat.cancel = func() { cancelled = true }
	ch := make(chan string, 1)
	m = inspectionUpdate(m, showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: strings.Repeat("full action\n", 70), answer: ch})
	m.Input.SetValue("hello")
	m = enter(m)
	if m.Input.Value() != "hello" || m.Chat.Dialog == nil {
		t.Fatal("Enter consumed draft or approved")
	}
	m.HelpOpen = true
	m.HelpScroll = 0
	m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if !m.HelpOpen || !strings.Contains(inspectionFrame(t, m), "Commands & keys") {
		t.Fatal("auth displaced Help priority")
	}
	m = inspectionUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.HelpOpen || m.Chat.Dialog == nil {
		t.Fatal("help close declined auth")
	}
	m.Input.SetValue("/stop")
	m = enter(m)
	if !cancelled || <-ch != "n" || m.Chat.Dialog != nil || !m.Chat.stopFlag {
		t.Fatal("public /stop did not settle owned approval")
	}
}
