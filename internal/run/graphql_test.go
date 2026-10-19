package run

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestModelGraphQLRegistered(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("lookup control unexpectedly requested inference") }))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	cfg.Decision.Server = srv.URL
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	bindDecisionFixture(t, &cfg, nil)
	r, _, e := New(Deps{Store: s, Cfg: cfg, Server: cfg.Model.Server, Tools: "full", SessionID: "actual-session", Model: "fixture", MaxSteps: 3, Window: 32768}, "inspect project notes")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := r.Registry.Lookup("graphql"); !ok {
		t.Fatal("full owning registry lacks actual GraphQL tool")
	}
}

func TestModelGraphQLPublicFlowAndApproval(t *testing.T) {
	for _, protocol := range []string{"structured", "text"} {
		t.Run(protocol, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			cfg.Model.ToolProtocol = protocol
			cfg.Model.Runtime = "openai"
			cfg.Coding.CheckCommand = "printf checked > check-marker"
			cfg.Auth.Mode = config.AuthAll
			cfg.Recall.TopN = 0
			srv := codingFixture(t, protocol, []codingCall{{"graphql", map[string]any{"query": `mutation { putKnowledge(key:"working/checks",body:"α🙂 authored",expectedPreviousId:"") {id body} }`}}, {"graphql", map[string]any{"query": `{ knowledge(key:"working/checks",limit:1) {id body} }`}}, {"check", map[string]any{"timeout": 2}}})
			// Capture the actual next request, keeping the canned model response fixture separate.
			u, _ := url.Parse(srv.URL)
			proxy := httputil.NewSingleHostReverseProxy(u)
			var requests [][]byte
			wire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				requests = append(requests, body)
				req.Body = io.NopCloser(bytes.NewReader(body))
				proxy.ServeHTTP(w, req)
			}))
			defer wire.Close()
			cfg.Model.Server, cfg.Decision.Server = wire.URL, wire.URL
			s, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			approvals := 0
			bindDecisionFixture(t, &cfg, nil)
			r, _, e := assignedCodingNew(t, Deps{Store: s, Cfg: cfg, Server: cfg.Model.Server, Tools: "full", Session: sink, SessionID: "graph-real", Model: "fixture", MaxSteps: 6, Window: 32768, AskYesNo: func(_ context.Context, p string) bool {
				approvals++
				if strings.Contains(p, `"tool":"graphql"`) && (!strings.Contains(p, "_project_root") || !strings.Contains(p, "_store") || !strings.Contains(p, "graph-real")) {
					t.Errorf("missing host-bound action %s", p)
				}
				return true
			}}, "record authored note and check")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = r.Run(context.Background(), "record authored note and check")
			if e != nil || approvals != 3 {
				t.Fatalf("actual flow %+v approvals%d %v", r.Outcome, approvals, e)
			}
			assertQualifiedTaskWithOpenParents(t, r, s)
			if len(requests) != 7 {
				t.Fatalf("actual requests %d", len(requests))
			}
			if !bytes.Contains(requests[5], []byte("VALIDATED task=")) {
				t.Fatal("qualified progress missing from final consuming request")
			}
			if !bytes.Contains(requests[6], []byte("The selected Plan is not complete")) {
				t.Fatal("premature final omitted the pending parent handoff")
			}
			var next struct {
				Messages []struct {
					Role, Content string
					ToolCallID    string `json:"tool_call_id"`
				}
			}
			if err := json.Unmarshal(requests[1], &next); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, msg := range next.Messages {
				if strings.Contains(msg.Content, "α🙂 authored") && ((protocol == "structured" && msg.Role == "tool" && msg.ToolCallID == "call0") || (protocol == "text" && msg.Role == "user")) {
					found = true
				}
			}
			if !found {
				t.Fatalf("actual correlated result missing from next wire: %s", requests[1])
			}
			marker, e := os.ReadFile("check-marker")
			if e != nil || string(marker) != "checked" {
				t.Fatal("actual check effect", e)
			}
			notes, e := memory.Knowledge(context.Background(), s, r.Workspace, "working/checks", "", 1, 65536, true)
			if e != nil || len(notes) != 1 || notes[0].Body != "α🙂 authored" || notes[0].SessionRef != "graph-real" {
				t.Fatalf("actual owner %v %v", notes, e)
			}
			events, e := session.Read(sink.dir, "graph-real")
			if e != nil {
				t.Fatal(e)
			}
			bound, results := 0, 0
			for _, ev := range events {
				if ev.Kind == "bound_call" && strings.Contains(ev.Text, "graphql") {
					bound++
				}
				if (ev.Kind == "result" || ev.Kind == "native_result") && strings.Contains(ev.Text, "α🙂 authored") {
					results++
				}
			}
			if bound != 2 || results != 2 {
				t.Fatalf("durable association bound%d results%d", bound, results)
			}
		})
	}
	t.Run("declined", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		cfg := config.Defaults()
		cfg.Model.ToolProtocol = "structured"
		cfg.Model.Runtime = "openai"
		cfg.Auth.Mode = config.AuthAll
		cfg.Recall.TopN = 0
		cfg.Coding.CheckCommand = "printf denied > marker"
		srv := codingFixture(t, "structured", []codingCall{{"graphql", map[string]any{"query": `mutation {putKnowledge(key:"denied",body:"no",expectedPreviousId:""){id}}`}}})
		cfg.Model.Server, cfg.Decision.Server = srv.URL, srv.URL
		s, e := store.Open(filepath.Join(dir, "db"))
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		bindDecisionFixture(t, &cfg, nil)
		r, _, e := New(Deps{Store: s, Cfg: cfg, Server: cfg.Model.Server, Tools: "full", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "denied", Model: "fixture", MaxSteps: 4, Window: 32768, AskYesNo: func(context.Context, string) bool { return false }}, "decline")
		if e != nil {
			t.Fatal(e)
		}
		r.Run(context.Background(), "decline")
		notes, e := memory.Knowledge(context.Background(), s, r.Workspace, "denied", "", 1, 65536, true)
		if e != nil || len(notes) != 0 {
			t.Fatal("declined effect", e)
		}
		if _, e = os.Stat("marker"); !os.IsNotExist(e) {
			t.Fatal("declined check effect", e)
		}
	})
}
