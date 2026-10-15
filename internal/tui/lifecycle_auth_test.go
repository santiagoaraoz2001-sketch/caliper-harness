package tui

import (
	"context"
	"errors"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestLifecycleWorkflowFactoryKeepsConfiguredAll(t *testing.T) {
	cfg := testCfg()
	cfg.Auth.Mode = config.AuthAll
	cfg.Sessions.Path = filepath.Join(t.TempDir(), "sessions")
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m := New(cfg, "component")
	d, e := m.wfLaunchDeps(s, func(context.Context, string) bool { return false })
	if e != nil {
		t.Fatal(e)
	}
	if d.AuthMode != config.AuthAll || d.ReviewGate == nil || d.Approve == nil {
		t.Fatal("actual TUI factory lost configured shared auth")
	}
}
func TestLifecycleManagedStopShowsStoppedAndReturnedDetail(t *testing.T) {
	m := New(testCfg(), "component")
	m.Chat.Running = true
	m.Chat.Seq = 1
	m.onRunDone(runDoneMsg{seq: 1, outcome: outcome.Outcome{Status: outcome.Stopped, Reason: "actual managed stop"}, runErr: errors.New("actual admission detail")})
	stopped, detail := false, false
	for _, line := range m.Chat.Lines {
		if strings.Contains(line.Text, "run failed:") {
			t.Fatal("Stopped relabeled Failed", line.Text)
		}
		stopped = stopped || strings.Contains(line.Text, "run stopped: actual managed stop")
		detail = detail || strings.Contains(line.Text, "actual admission detail")
	}
	if !stopped || !detail {
		t.Fatal("terminal outcome or returned detail lost", m.Chat.Lines)
	}
}
