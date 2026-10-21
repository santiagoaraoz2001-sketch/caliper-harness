package tui

import (
	"context"
	"strings"
	"testing"
)

func TestCancellationNoticeRemainsTruthfulAfterOwnerSettlement(t *testing.T) {
	for _, kind := range []string{"chat", "compaction", "workflow"} {
		t.Run(kind, func(t *testing.T) {
			m := New(testCfg(), "cancellation")
			m.Width, m.Height = 120, 36
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m.alert("warn", "older unrelated warning")
			want := "stop requested"
			switch kind {
			case "chat":
				m.Chat.Running, m.Chat.Seq, m.Chat.cancel = true, 7, cancel
			case "compaction":
				m.Chat.Compacting, m.Chat.compactCancel = true, cancel
				want = "compaction stop requested"
			case "workflow":
				m.Wf.Busy, m.Wf.ctx, m.Wf.cancel = "owned", ctx, cancel
				want = "workflow stop requested"
			}
			owner := m.origin()
			m.stopChat()
			if ctx.Err() == nil || !m.modelWorkActive() {
				t.Fatal("stop released owner before settlement")
			}
			m.alert("err", "newer independent error")
			before := len(m.Alerts)
			switch kind {
			case "chat":
				m.onRunDone(runDoneMsg{seq: 6})
				if !m.Chat.Running || len(m.Alerts) != before {
					t.Fatal("stale completion changed current run")
				}
				m.onRunDone(runDoneMsg{seq: 7, runErr: context.Canceled})
			case "compaction":
				m.onCompactDone(compactDoneMsg{origin: owner, ctx: ctx, runErr: context.Canceled})
			case "workflow":
				m.onWfDone(wfDoneMsg{ctx: context.Background(), approach: "other"})
				if m.Wf.Busy != "owned" || len(m.Alerts) != before {
					t.Fatal("stale completion changed current workflow")
				}
				m.onWfDone(wfDoneMsg{ctx: ctx, approach: "owned", err: context.Canceled})
			}
			if m.modelWorkActive() {
				t.Fatal("matching settlement retained busy state")
			}
			if len(m.Alerts) < 3 || m.Alerts[0].Text != "older unrelated warning" || m.Alerts[1].Text != want || m.Alerts[2].Text != "newer independent error" {
				t.Fatalf("request history or unrelated notices changed: %+v", m.Alerts)
			}
			if kind != "chat" && m.Alerts[len(m.Alerts)-1].Text != context.Canceled.Error() {
				t.Fatal("settlement cancellation detail lost")
			}
			// Dismiss the newer notices through the real key route so the retained
			// stop-request event is visible, as in the live failure.
			for range len(m.Alerts) - 2 {
				m = key(m, "esc")
			}
			if !strings.Contains(m.View(), want) {
				t.Fatalf("settled notification not rendered: %s", m.View())
			}
			state, _ := m.activityState()
			if state == "Stopping" || state == "Running" {
				t.Fatalf("settled activity still claims %q", state)
			}
			t.Logf("settled %s: rendered notice=%q activity=%q active=false; other notices preserved", kind, want, state)
			for _, stale := range []string{"waiting for owned work to settle", "waiting for settlement"} {
				if strings.Contains(m.View(), stale) {
					t.Fatalf("settled view still claims %q", stale)
				}
			}
		})
	}
}
