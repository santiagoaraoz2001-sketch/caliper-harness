package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

func TestIntentionalStreamCancellationKeepsPartialWithoutServerRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var requests, effects atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"model\":\"cancel-fixture\",\"choices\":[{\"delta\":{\"reasoning\":\"retained raw thought α\",\"content\":\"unfinished output\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	}))
	defer func() { cancel(); srv.Close() }()
	m := New(testCfg(), "cancellation")
	m.Chat.Running, m.Chat.Seq, m.Chat.cancel = true, 3, cancel
	m.Cfg.Tabs.TitleEvery = 0
	messages := make(chan tea.Msg, 100)
	rawDir := t.TempDir()
	log := fanoutLog{logs: []agent.SessionLog{displayRawSession{rawDir}, progLog{seq: 3, send: func(msg tea.Msg) { messages <- msg }}}}
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "effect", Purpose: "must not execute", Execute: func(context.Context, tool.Args) (tool.Result, error) { effects.Add(1); return tool.Result{}, nil }})
	r := &agent.Runner{Model: llm.New(srv.URL, "cancel-fixture").WithRuntime("mlx"), Registry: reg, Session: log, SessionID: "cancel-stream", OnToken: func(s string) { messages <- tokenMsg{seq: 3, delta: s} }}
	done := make(chan runDoneMsg, 1)
	go func() {
		final, _, err := r.Run(ctx, "retain original intent")
		done <- runDoneMsg{seq: 3, final: final, continuation: r.Continuation, in: r.UsedIn, out: r.UsedOut, unknown: r.UsageUnknown, outcome: r.Outcome, runErr: err}
	}()
	// Cancel only after the actual client has received and displayed the partial.
	for m.Chat.Stream.String() != "unfinished output" {
		select {
		case msg := <-messages:
			next, _ := m.Update(msg)
			m = next.(Model)
		case <-time.After(3 * time.Second):
			t.Fatal("partial never reached UI")
		}
	}
	m.stopChat()
	var settled runDoneMsg
	select {
	case settled = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not settle")
	}
	for len(messages) > 0 {
		next, _ := m.Update(<-messages)
		m = next.(Model)
	}
	m.onRunDone(settled)
	if !errors.Is(settled.runErr, context.Canceled) || settled.outcome.Status != outcome.Cancelled || !settled.unknown || m.Chat.Running || requests.Load() != 1 || effects.Load() != 0 {
		t.Fatalf("cancel contract: %+v requests=%d effects=%d", settled, requests.Load(), effects.Load())
	}
	raw, err := os.ReadFile(filepath.Join(rawDir, "cancel-stream.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	saved := string(raw)
	for _, want := range []string{"retained raw thought", "unfinished output", "partial", "cancelled", "unknown (not reported by server)"} {
		if !strings.Contains(saved, want) {
			t.Errorf("raw log lost %q", want)
		}
	}
	var visible strings.Builder
	for _, line := range m.Chat.Lines {
		visible.WriteString(line.Text)
		visible.WriteByte('\n')
	}
	for _, text := range []string{settled.runErr.Error(), saved, visible.String()} {
		for _, bad := range []string{"check the server", "and retry", "stream from"} {
			if strings.Contains(text, bad) {
				t.Errorf("intentional stop suggests server failure/retry: %s", text)
			}
		}
	}
	if !strings.Contains(visible.String(), "stopped.") {
		t.Errorf("UI lacks settled stop: %s", visible.String())
	}
}
