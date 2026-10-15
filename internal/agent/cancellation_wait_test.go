package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tools/fetch"
	"sync"
	"testing"
	"time"
)

type r04PromptOutput struct {
	bytes.Buffer
	once  sync.Once
	ready chan struct{}
}

func (p *r04PromptOutput) Write(b []byte) (int, error) {
	n, e := p.Buffer.Write(b)
	if bytes.Contains(b, []byte("answer:")) {
		p.once.Do(func() { close(p.ready) })
	}
	return n, e
}
func TestR04CancelReleasesOwnedAsk(t *testing.T) {
	rd, wr := io.Pipe()
	defer rd.Close()
	defer wr.Close()
	output := &r04PromptOutput{ready: make(chan struct{})}
	reg := tool.NewRegistry()
	reg.Register(ask.New(&ask.Prompter{In: ask.NewInput(rd), Out: output}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runner{Model: &cancellationStimulus{reply: "```tool\ntool: ask\nquestion: cancellation probe\n```"}, Registry: reg, MaxSteps: 1, Out: io.Discard}
	done := make(chan struct{})
	go func() { r.Run(ctx, "ask probe"); close(done) }()
	select {
	case <-output.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("owned ask never prompted")
	}
	cancel()
	settled := false
	select {
	case <-done:
		settled = true
	case <-time.After(150 * time.Millisecond):
	}
	// Explicit owned-pipe cleanup releases original synchronous ReadString; never user stdin.
	wr.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ask cleanup failed")
	}
	cancellationObserve(t, "blocked-ask", map[string]any{"released_on_cancellation": settled, "release_control": "owned writer explicitly closed after cancelled reader settlement", "prompt": output.Buffer.String(), "inference": false})
	if !settled {
		t.Error("Runner cancellation left admitted Ask blocked until owned input was closed")
	}
}
func TestR04CancelReachesFetchRequest(t *testing.T) {
	started := make(chan *http.Request, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fixture complete"))
	}))
	defer srv.Close()
	reg := tool.NewRegistry()
	reg.Register(fetch.New(config.Defaults().Tools.Fetch))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runner{Model: &cancellationStimulus{reply: "```tool\ntool: fetch\nurl: " + srv.URL + "\n```"}, Registry: reg, MaxSteps: 1, Out: io.Discard}
	done := make(chan struct{})
	go func() { r.Run(ctx, "fetch probe"); close(done) }()
	var request *http.Request
	select {
	case request = <-started:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("owned fixture request missing")
	}
	cancel()
	requestCancelled := false
	select {
	case <-request.Context().Done():
		requestCancelled = true
	case <-time.After(150 * time.Millisecond):
	}
	settled := false
	select {
	case <-done:
		settled = true
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch cleanup failed")
	}
	cancellationObserve(t, "blocked-fetch", map[string]any{"request_cancelled_with_runner": requestCancelled, "runner_settled_before_fixture_release": settled, "fixture": "ephemeral owned httptest, no model/service forward", "inference": false})
	if !requestCancelled || !settled {
		t.Error("Runner cancellation did not cancel admitted Fetch HTTP request")
	}
}
