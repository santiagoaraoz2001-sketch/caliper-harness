package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/outcome"
	"strings"
	"testing"
)

func scrollFixture() Model {
	m := New(testCfg(), "scroll")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	for i := 0; i < 80; i++ {
		m.Chat.appendLine(lineResult, fmt.Sprintf("SOURCE_%03d repeated words retained in exact output", i))
	}
	m.Input.SetValue("draft retained")
	return m
}
func TestChatWheelKeepsChromeAndComposer(t *testing.T) {
	m := scrollFixture()
	before := strings.Split(ansi.Strip(m.View()), "\n")
	next, _ := m.Update(tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 2, Button: tea.MouseButtonWheelUp})
	m = next.(Model)
	if !m.Chat.pinned {
		t.Fatal("wheel does not scroll transcript")
	}
	after := strings.Split(ansi.Strip(m.View()), "\n")
	if before[0] != after[0] || before[len(before)-7] != after[len(after)-7] || m.Input.Value() != "draft retained" {
		t.Fatal("scroll moved chrome or changed draft")
	}
	top := m.Chat.pinTop
	next, _ = m.Update(tea.MouseMsg{X: 8, Y: 0, Button: tea.MouseButtonWheelUp})
	m = next.(Model)
	if m.Chat.pinTop != top {
		t.Fatal("header wheel scrolls hidden content")
	}
	next, _ = m.Update(tea.MouseMsg{X: 8, Y: m.Height - 4, Button: tea.MouseButtonWheelUp})
	m = next.(Model)
	if m.Chat.pinTop != top {
		t.Fatal("composer wheel scrolls hidden content")
	}
	m.HelpOpen = true
	next, _ = m.Update(tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 2, Button: tea.MouseButtonWheelUp})
	m = next.(Model)
	if m.Chat.pinTop != top {
		t.Fatal("help lost input ownership")
	}
	m.HelpOpen = false
	for i := 0; i < 100; i++ {
		next, _ = m.Update(tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 2, Button: tea.MouseButtonWheelDown})
		m = next.(Model)
	}
	if m.Chat.pinned || !strings.Contains(ansi.Strip(m.View()), "SOURCE_079") {
		t.Fatal("wheel cannot return to live tail")
	}
}
func TestChatReadingSurvivesRunCompletion(t *testing.T) {
	m := scrollFixture()
	m.Chat.Running = true
	m.Chat.Streaming = true
	m.Chat.Seq = 9
	m.Chat.Stream.WriteString("new assistant reply")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	before := m.Chat.pinTop
	next, _ = m.Update(runDoneMsg{seq: 9, outcome: outcome.Outcome{Status: outcome.Completed}})
	m = next.(Model)
	if !m.Chat.pinned || m.Chat.pinTop != before {
		t.Fatal("run completion forced reading to live tail")
	}
	if m.Input.Value() != "draft retained" {
		t.Fatal("completion changed draft")
	}
}
