package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/config"
	appupdate "specific-harness/internal/update"
)

func TestUpdateProgressIsOneRowAndIgnoresOldAttempt(t *testing.T) {
	m := New(config.Defaults(), "0.2.3")
	m.Width, m.Height = 100, 30
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.Updater = &updateState{busy: true, ctx: ctx, cancel: cancel}
	before := len(m.Chat.Lines)
	height := viewHeight(m)
	for i := 1; i <= 100; i++ {
		m.updateProgress(updateProgressMsg{ctx, appupdate.Progress{Phase: "Downloading update", Bytes: int64(i) << 20, TotalBytes: 200 << 20, Elapsed: time.Duration(i) * time.Second}})
	}
	if len(m.Chat.Lines) != before {
		t.Fatal("progress bloated transcript")
	}
	if !strings.Contains(m.updateProgressLine(), "100.0 MiB / 200.0 MiB") || !strings.Contains(m.View(), "Update: Downloading update") {
		t.Fatal("actual progress not visible", m.updateProgressLine())
	}
	if viewHeight(m) != height-1 {
		t.Fatal("progress row did not reserve space")
	}
	m.Updater = &updateState{busy: true, ctx: context.Background()}
	m.updateProgress(updateProgressMsg{ctx, appupdate.Progress{Phase: "old attempt"}})
	if m.updateProgressLine() != "" {
		t.Fatal("old attempt changed current display")
	}
	m.Updater = &updateState{}
	m.updateProgress(updateProgressMsg{ctx, appupdate.Progress{Phase: "old attempt"}})
	if m.updateProgressLine() != "" {
		t.Fatal("late event resurrected progress")
	}
}
