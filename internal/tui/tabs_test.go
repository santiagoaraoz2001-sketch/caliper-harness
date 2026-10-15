package tui

import (
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

// tabHistory plants n user turns on the active chat for isolation
// probes: history is the per-tab session state.
func tabHistory(m *Model, tag string, n int) {
	for i := 0; i < n; i++ {
		m.Chat.History = append(m.Chat.History,
			llm.Message{Role: "user", Content: tag},
			llm.Message{Role: "assistant", Content: tag},
		)
	}
}

func TestTabChatsIsolated(t *testing.T) {
	m := New(testCfg(), "test")
	tabHistory(&m, "aaa", 1)
	m = key(m, "ctrl+t")
	if len(m.Chat.History) != 0 {
		t.Fatalf("new tab must start fresh, got %d msgs", len(m.Chat.History))
	}
	if m.Chat.Stream == nil {
		t.Fatal("new tab chat must be writable")
	}
	tabHistory(&m, "bbb", 2)
	m = key(m, "ctrl+n")
	if len(m.Chat.History) != 2 || m.Chat.History[0].Content != "aaa" {
		t.Fatalf("tab A lost its session: %+v", m.Chat.History)
	}
	m = key(m, "ctrl+n")
	if len(m.Chat.History) != 4 || m.Chat.History[0].Content != "bbb" {
		t.Fatalf("tab B lost its session: %+v", m.Chat.History)
	}
}

func TestTabSwitchRefusedMidRun(t *testing.T) {
	m := New(testCfg(), "test")
	tabHistory(&m, "aaa", 1)
	m = key(m, "ctrl+t")
	m.Chat.Running = true
	m.SyncTabChat()
	n := len(m.Alerts)
	m = key(m, "ctrl+n")
	if m.Active != 1 {
		t.Fatal("switch mid-run must refuse")
	}
	if len(m.Alerts) != n+1 || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "/stop") {
		t.Fatalf("refusal must say how to proceed: %+v", m.Alerts)
	}
}

func TestTabNewRefusedMidRun(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m = key(m, "ctrl+t")
	if len(m.Tabs) != 1 {
		t.Fatal("new tab mid-run must refuse")
	}
}

func TestTabCloseRefusedMidRun(t *testing.T) {
	m := New(testCfg(), "test")
	m = key(m, "ctrl+t")
	m.Chat.Running = true
	m = key(m, "ctrl+w")
	if len(m.Tabs) != 2 || m.Active != 1 {
		t.Fatal("close mid-run must refuse")
	}
}

func TestTabCloseRestores(t *testing.T) {
	m := New(testCfg(), "test")
	tabHistory(&m, "aaa", 1)
	m = key(m, "ctrl+t")
	tabHistory(&m, "bbb", 2)
	m = key(m, "ctrl+w")
	if m.Active != 0 || len(m.Tabs) != 1 {
		t.Fatalf("close must land on tab A: active=%d tabs=%d", m.Active, len(m.Tabs))
	}
	if len(m.Chat.History) != 2 || m.Chat.History[0].Content != "aaa" {
		t.Fatalf("close must restore tab A session: %+v", m.Chat.History)
	}
}

func TestTabNewChatStaysOnTab(t *testing.T) {
	m := New(testCfg(), "test")
	tabHistory(&m, "aaa", 1)
	m = key(m, "ctrl+t")
	tabHistory(&m, "bbb", 1)
	m.newChat()
	if len(m.Chat.History) != 0 {
		t.Fatal("/new must clear the active tab")
	}
	m = key(m, "ctrl+p")
	if len(m.Chat.History) != 2 || m.Chat.History[0].Content != "aaa" {
		t.Fatalf("/new must not touch the other tab: %+v", m.Chat.History)
	}
}

func TestTabBareViewStartsFresh(t *testing.T) {
	m := New(testCfg(), "test")
	m.Tabs = append(m.Tabs, Tab{at: place{view: ViewDiff}})
	m = key(m, "ctrl+n")
	if m.Chat.Stream == nil {
		t.Fatal("landing on a chatless tab must still be writable")
	}
	m.Chat.appendLine(lineUser, "hello")
	if len(m.Chat.Lines) != 1 {
		t.Fatal("fresh tab chat must accept lines")
	}
}

func TestResumeSeedSurvivesSwitch(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.History = []llm.Message{{Role: "user", Content: "old"}}
	m.SyncTabChat()
	m = key(m, "ctrl+t")
	m = key(m, "ctrl+p")
	if len(m.Chat.History) != 1 || m.Chat.History[0].Content != "old" {
		t.Fatalf("resume seed must survive a switch cycle: %+v", m.Chat.History)
	}
}
