package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/outcome"
)

func TestCancellationUISettlementNewQuitAndStaleDialog(t *testing.T) {
	m := New(testCfg(), "component")
	ctx, cancel := context.WithCancel(context.Background())
	m.Chat.Running, m.Chat.Seq, m.Chat.cancel = true, 7, cancel
	m.Chat.appendLine(lineUser, "retain until settled")
	m.newChat()
	if !m.Chat.Running || m.Chat.Seq != 7 || len(m.Chat.Lines) != 1 || ctx.Err() == nil {
		t.Fatal("new declared idle before settlement")
	}
	answer := make(chan string, 1)
	u, _ := m.Update(showDialogMsg{seq: 7, ctx: ctx, kind: "auth", answer: answer})
	m = u.(Model)
	if m.Chat.Dialog != nil || <-answer != "n" {
		t.Fatal("cancelled dialog became visible")
	}
	m.onRunDone(runDoneMsg{seq: 7, outcome: outcome.Outcome{Status: outcome.Cancelled}})
	if m.Chat.Running || m.Chat.Seq != 8 || len(m.Chat.Lines) != 0 {
		t.Fatal("queued new did not land after settlement")
	}
	ctx, cancel = context.WithCancel(context.Background())
	m.Chat.Running, m.Chat.cancel = true, cancel
	m.Wf.Busy = "owned-workflow"
	if cmd := m.requestQuit(); cmd != nil || ctx.Err() == nil {
		t.Fatal("quit bypassed owned work")
	}
	if cmd := m.onRunDone(runDoneMsg{seq: m.Chat.Seq}); cmd != nil {
		t.Fatal("quit bypassed workflow settlement")
	}
	cmd := m.onWfDone(wfDoneMsg{})
	if cmd == nil {
		t.Fatal("settled quit did not exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("unexpected quit command")
	}
}

// Escape during a run stops generation even when a detail view is open
// over it: the stop request must not be consumed by closing that view.
func TestEscapeStopsRunBeforeDetailView(t *testing.T) {
	m := New(testCfg(), "component")
	ctx, cancel := context.WithCancel(context.Background())
	m.Chat.Running, m.Chat.Seq, m.Chat.cancel = true, 3, cancel
	m.Tabs[m.Active].at.view = ViewFiles
	m.Files.Path = "some/file.go"
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if ctx.Err() == nil {
		t.Fatal("escape did not cancel the running generation")
	}
	if m.Files.Path == "" {
		t.Fatal("escape closed the detail view instead of stopping the run")
	}
}
