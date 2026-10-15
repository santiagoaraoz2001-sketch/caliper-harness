package serve

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

type startupBlockedTransport struct{ entered chan struct{} }

func (s startupBlockedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	close(s.entered)
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestStartupHealthProbeCancelsInFlightRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan Health, 1)
	client := &http.Client{Timeout: 30 * time.Second, Transport: startupBlockedTransport{entered}}
	go func() { done <- ProbeContext(ctx, client, Endpoint{Kind: KindSidecar, URL: "http://127.0.0.1:1"}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case health := <-done:
		if health.Up || health.Reachable || !strings.Contains(health.Err, "context canceled") {
			t.Fatal(health)
		}
	case <-time.After(time.Second):
		t.Fatal("startup cancellation waited for the HTTP timeout")
	}
}
