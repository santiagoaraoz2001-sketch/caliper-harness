package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func restoredCostModel(t *testing.T) Model {
	t.Helper()
	cfg := testCfg()
	cfg.Context.Keep = .01
	dir := t.TempDir()
	cfg.Store.Path, cfg.Sessions.Path = dir+"/kuzu", dir+"/sessions"
	m := New(cfg, "test")
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "original task", false)
	prior := "prior work" + strings.Repeat(" consumed prior draft", 40)
	c.Add("reply", "assistant", prior, false)
	encoded, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	id := session.NewID()
	for _, event := range []session.Event{{Kind: "prompt", Text: "original task"}, {Kind: "reply", Text: prior}, {Kind: "continuation", Text: encoded}} {
		if err := session.Append(cfg.Sessions.Path, id, event); err != nil {
			t.Fatal(err)
		}
	}
	m.Chat.Context, _, err = BaselineContinuation(cfg.Sessions.Path, id, m.St.CtxWindow, m.contextKey())
	if err != nil || len(m.Chat.Context.Entries) == 0 {
		t.Fatalf("restore: %v, %+v", err, m.Chat.Context)
	}
	m.Chat.SessionIDs = []string{id}
	return m
}

func updateCostModel(m *Model, msg tea.Msg) tea.Cmd {
	next, cmd := m.Update(msg)
	*m = next.(Model)
	return cmd
}

func costReadout(t *testing.T, m *Model) string {
	t.Helper()
	m.Input.SetValue("/context")
	updateCostModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.Alerts) == 0 {
		t.Fatal("context produced no readout")
	}
	return m.Alerts[len(m.Alerts)-1].Text
}

func TestRestoredCostUnavailableUntilNewRun(t *testing.T) {
	m := restoredCostModel(t)
	if got := costReadout(t, &m); !strings.Contains(got, "prior run cost unavailable after resume") || strings.Contains(got, "0 in, 0 out") || !strings.Contains(got, "prompt: unknown") {
		t.Fatalf("missing counters presented as measured historical cost: %s", got)
	}
	for _, tc := range []struct {
		name               string
		in, out            int
		unknown, cancelled bool
		status             outcome.Status
	}{
		{"completed", 300, 50, false, false, outcome.Completed},
		{"failed-partial", 120, 7, true, false, outcome.Failed},
		{"cancelled", 23, 4, false, true, outcome.Cancelled},
		{"zero-cost", 0, 0, false, false, outcome.Failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := restoredCostModel(t)
			c := m.Chat.Context.Clone()
			c.Measured, c.Prompt, c.ModelKey = true, 200, m.contextKey()
			m.Chat.Running, m.Chat.stopFlag = true, tc.cancelled
			updateCostModel(&m, runDoneMsg{seq: m.Chat.Seq, continuation: c, in: tc.in, out: tc.out, unknown: tc.unknown, outcome: outcome.Outcome{Status: tc.status}})
			got := costReadout(t, &m)
			cost := fmt.Sprintf("cumulative last-run cost: %d in, %d out", tc.in, tc.out)
			if !strings.Contains(got, cost) || strings.Contains(got, "unavailable") || !strings.Contains(got, "prompt: 200 / ") || strings.Contains(got, "unreported") != tc.unknown {
				t.Fatalf("new settlement accounting/provenance: %s", got)
			}
		})
	}
}

func TestRestoredCostManualCompactionAndTabOwnership(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Pending source checks remain unverified.\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":600}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	m := restoredCostModel(t)
	m.Cfg.Decision = transportCompactModel(t, srv.URL+"/v1").Cfg.Decision
	m.Server, m.ModelName, m.Cfg.Model.Runtime = srv.URL+"/v1", "fixture", "openai"
	m.Cfg.Templates.Path = "../../templates"
	m.St.CtxWindow = 32768
	cmd := m.compactChat()
	if cmd == nil {
		t.Fatal(m.Alerts)
	}
	message := cmd().(compactDoneMsg)
	if message.runErr != nil {
		t.Fatal(message.runErr)
	}
	m.newTab()
	save := updateCostModel(&m, message)
	if save == nil {
		t.Fatal("compaction persistence was not admitted")
	}
	saved := save().(compactDoneMsg)
	if saved.runErr != nil {
		t.Fatal(saved.runErr)
	}
	updateCostModel(&m, saved)
	if got := costReadout(t, &m); got != "no run yet" {
		t.Fatalf("fresh tab inherited origin cost: %s", got)
	}
	m.switchTab(0)
	for _, action := range []string{"/context", "/clear"} {
		m.Input.SetValue(action)
		updateCostModel(&m, tea.KeyMsg{Type: tea.KeyEnter})
		got := costReadout(t, &m)
		if !strings.Contains(got, "prior run cost unavailable after resume") || !strings.Contains(got, "new compaction cost since resume: 120 in, 600 out") || strings.Contains(got, "cumulative last-run cost:") {
			t.Fatalf("compaction invented history or double charged (%s): %s", action, got)
		}
	}
	// A partial failed new compaction contributes only its actual reported cost.
	updateCostModel(&m, compactDoneMsg{ctx: context.Background(), origin: m.origin(), in: 23, out: 7, unknown: true, runErr: fmt.Errorf("controlled failure")})
	if got := costReadout(t, &m); !strings.Contains(got, "new compaction cost since resume: 143 in, 607 out (some exchanges unreported)") || !strings.Contains(got, "prior run cost unavailable") {
		t.Fatalf("partial compaction scope: %s", got)
	}
	m.Chat.Running = true
	updateCostModel(&m, runDoneMsg{seq: m.Chat.Seq, continuation: m.Chat.Context.Clone(), in: 40, out: 8})
	m.newTab()
	if got := costReadout(t, &m); got != "no run yet" {
		t.Fatalf("new tab inherited actual run cost: %s", got)
	}
	m.switchTab(0)
	m.Input.SetValue("/clear")
	updateCostModel(&m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := costReadout(t, &m); !strings.Contains(got, "cumulative last-run cost: 40 in, 8 out") || strings.Contains(got, "unavailable") || strings.Contains(got, "unreported") || strings.Contains(got, "compaction cost") {
		t.Fatalf("new run did not replace prior compaction accounting or survive tab/clear: %s", got)
	}
	m.Input.SetValue("/new")
	updateCostModel(&m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := costReadout(t, &m); got != "no run yet" {
		t.Fatalf("new conversation retained cost provenance: %s", got)
	}
}
