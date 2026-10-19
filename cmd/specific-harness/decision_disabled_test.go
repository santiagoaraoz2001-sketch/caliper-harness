package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/config"
	"strings"
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

func TestExplicitExternalDecisionStartupDoesNotLaunch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := server.URL
	server.Close() // A configured but stopped external service must stay external.
	cfg := config.Defaults()
	cfg.Decision.Server = endpoint
	cfg.Serve.Recipes = []config.RecipeConfig{{Name: "external-judge", Kind: "sidecar", Runtime: cfg.Decision.Runtime, URL: endpoint}}
	var notices []string
	if p := ensureSidecarContext(context.Background(), cfg, func(s string) { notices = append(notices, s) }); p != nil {
		defer p.Stop()
		t.Fatal("explicit external service started")
	}
	if !strings.Contains(strings.Join(notices, "\n"), "server is external") {
		t.Fatalf("external service misreported: %v", notices)
	}
}
