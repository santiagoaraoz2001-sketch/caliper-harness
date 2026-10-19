package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/config"
	"sync/atomic"
	"testing"
)

func TestDisabledDecisionStartupDoesNotProbeOrLaunch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	cfg := config.Defaults()
	cfg.Decision.Disabled = true
	cfg.Decision.Server = server.URL
	if p := ensureSidecarContext(context.Background(), cfg, func(string) { t.Fatal("disabled service attempted startup") }); p != nil {
		defer p.Stop()
		t.Fatal("disabled service started")
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled service probed %d times", calls.Load())
	}
}
