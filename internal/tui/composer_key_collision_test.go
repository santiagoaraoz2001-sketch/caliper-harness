package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/tools/ask"
	"strings"
	"testing"
)

// These are literal decoder packets, deliberately not key(m,"right").
func composerPacket(m Model, text string, paste bool) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: paste})
	return next.(Model)
}

func TestComposerPrintableKeyNames(t *testing.T) {
	for _, name := range []string{"right", "left", "up", "down", "home", "end", "delete", "backspace", "enter", "pgup", "pgdown", "ctrl+t", "ctrl+n", "ctrl+p", "ctrl+w", "ctrl+o", "ctrl+g", "alt+f"} {
		t.Run(name, func(t *testing.T) {
			m := New(testCfg(), "test")
			m.Input.SetValue("prefix  suffix")
			m.Input.SetCursor(7)
			m.Chat.pinned, m.Chat.pinTop = true, 4
			m.Hist = []string{"history"}
			m = composerPacket(m, name, false)
			if m.Input.Value() != "prefix "+name+" suffix" || len(m.Tabs) != 1 || m.Active != 0 || !m.Chat.pinned || m.Chat.pinTop != 4 || m.HistIdx != -1 {
				t.Fatalf("printable %q consumed as binding: input=%q tabs=%d active=%d pin=%v/%d history=%d", name, m.Input.Value(), len(m.Tabs), m.Active, m.Chat.pinned, m.Chat.pinTop, m.HistIdx)
			}
		})
	}
	sentence := "Fix the right answer α"
	for _, wordPackets := range []bool{true, false} {
		m := New(testCfg(), "test")
		if wordPackets {
			for i, word := range strings.Fields(sentence) {
				if i > 0 {
					next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
					m = next.(Model)
				}
				m = composerPacket(m, word, false)
			}
		} else {
			m = composerPacket(m, sentence, false)
		}
		if m.Input.Value() != sentence {
			t.Fatalf("packetized sentence changed: %q", m.Input.Value())
		}
	}
	m := composerPacket(New(testCfg(), "test"), "right left\n\tctrl+t α", true)
	if m.Input.Value() != "right left\n\tctrl+t α" {
		t.Fatalf("bracketed paste changed: %q", m.Input.Value())
	}
}

func TestComposerSpecialAndAltKeys(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("AB")
	m.Input.SetCursor(1)
	m = composerPacket(m, "right", false)
	m = key(m, "left") // actual KeyLeft still moves the cursor
	m = composerPacket(m, "X", false)
	if m.Input.Value() != "ArighXtB" {
		t.Fatalf("middle insertion/actual left: %q", m.Input.Value())
	}
	m = key(m, "right")
	if m.Input.Value() != "ArighXtB" {
		t.Fatal("actual right inserted text")
	}
	m = composerPacket(m, "Y", false)
	if m.Input.Value() != "ArighXtYB" {
		t.Fatal("actual right stopped moving the cursor")
	}
	m.Input.SetValue("one two")
	m.Input.CursorStart()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true})
	m = composerPacket(next.(Model), "X", false)
	if m.Input.Value() != "oneX two" {
		t.Fatalf("Alt word shortcut changed: %q", m.Input.Value())
	}
	m = key(m, "ctrl+t")
	if len(m.Tabs) != 2 || m.Active != 1 || m.Input.Value() != "" {
		t.Fatal("actual CtrlT lost tab ownership")
	}
	m = key(m, "ctrl+p")
	if m.Active != 0 || m.Input.Value() != "oneX two" {
		t.Fatal("actual CtrlP lost draft")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT, Alt: true})
	if len(next.(Model).Tabs) != 2 {
		t.Fatal("Alt+CtrlT became a plain CtrlT shortcut")
	}
}

func TestComposerAskKeyOwnership(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("draft β")
	m, done := labelsAsk(m, []ask.Question{{Text: "Literal answer?"}})
	m = composerPacket(m, "right", false)
	m = enter(m)
	select {
	case answers := <-done:
		if len(answers) != 1 || answers[0].Picked != "right" || m.Input.Value() != "draft β" {
			t.Fatalf("Ask/composer ownership: %#v input=%q", answers, m.Input.Value())
		}
	default:
		t.Fatal("printable answer was not delivered")
	}
}

func TestComposerWorkflowKeyOwnership(t *testing.T) {
	m := New(testCfg(), "test")
	m.Tabs[m.Active].at.view = ViewWorkflows
	m.Wf.Rows = []WfRow{{Approach: "selected-approach", Title: "selected"}}
	m = composerPacket(m, "enter", false)
	if m.Input.Value() != "enter" || m.Wf.Dive != "" || m.Tabs[m.Active].at.view != ViewWorkflows {
		t.Fatal("printable enter dived instead of typing")
	}
	m.Input.Reset()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.Wf.Dive != "selected-approach" || m.Tabs[m.Active].at.view != ViewPlans || cmd == nil {
		t.Fatal("actual Enter no longer dives")
	}
	// Deliberate view letters and Help priority remain controls, not composer text.
	m.Tabs[m.Active].at.view = ViewWorkflows
	m = composerPacket(m, "c", false)
	if !m.Wf.ShowDone || m.Input.Value() != "" {
		t.Fatal("intentional workflow c lost ownership")
	}
	m.HelpOpen = true
	m = composerPacket(m, "right", false)
	if m.HelpOpen || m.Input.Value() != "" {
		t.Fatal("Help dismissal leaked into composer")
	}
}
