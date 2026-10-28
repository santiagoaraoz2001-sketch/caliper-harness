package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/pathutil"
	"specific-harness/internal/run"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	graphTool "specific-harness/internal/tools/graph"
	"strings"
	"testing"
)

func TestTestsDesignFirstProviderPacketAndPublicEvidence(t *testing.T) {
	// Match production project capture before seeding or recording ownership.
	root, err := pathutil.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	db, dir := filepath.Join(t.TempDir(), "db"), t.TempDir()
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Decision.Disabled = true
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Sessions.Path = dir
	spec, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/testing/smoke", "Expected unique Δ result; reject a broken public entry.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/testing/strategy", "Keep real public behavior assertions; no golden self-copy.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	suite := config.TestSuite{ID: "smoke", Name: "Smoke", Purpose: "Public behavior", Expected: "observed Δ", Command: "printf 'observed'", Enabled: true, SpecKey: spec.Key, SpecVersion: spec.ID}
	cfg.Testing.Suites = []config.TestSuite{suite}
	cfg.Coding.CheckCommand = "printf required"
	deps := run.TestDeps{Cfg: cfg, Store: s, Workspace: root, Session: graph.NewSessionLog(graph.New(s, "tests-fixture", dir))}
	selection, err := run.FreezeTests(t.Context(), deps, []string{"smoke"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := run.RunTests(t.Context(), deps, selection)
	if err != nil || prior.Status != "passed" {
		t.Fatalf("real standalone execution: %+v %v", prior, err)
	}
	s.Close()
	m := New(cfg, "capture")
	m.StorePath, m.SessionsPath = db, dir
	m.Tests.Sel = 1
	msg := m.refreshTests()().(testsHistoryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m = viewportUpdate(m, msg)
	if m.Tests.Specs["smoke"].Body != spec.Body {
		t.Fatalf("refresh lost immutable spec: %+v", m.Tests.Specs)
	}
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := graphTool.New(graph.New(s, "readback", dir), cfg.Tools.GraphQL, root, "readback", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`{ sessionEvidence(sessionId:%q,offset:0,limit:20) { historical nextOffset groups { reference records { kind source workspace text } } } }`, prior.Selection.ID)
	result, err := d.Execute(t.Context(), tool.Args{"query": query})
	if err != nil || result.Failed || !strings.Contains(result.Text, "test_result") || !strings.Contains(result.Text, "never Chat/PAT qualification") || !strings.Contains(result.Text, "observed") {
		t.Fatalf("model evidence route: %s %v", result.Text, err)
	}
	foreign, err := graphTool.New(graph.New(s, "foreign", dir), cfg.Tools.GraphQL, t.TempDir(), "foreign", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	result, err = foreign.Execute(t.Context(), tool.Args{"query": query})
	if err == nil && !result.Failed {
		t.Fatal("foreign project read Tests evidence")
	}
	captured := make(chan []llm.Message, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Messages []llm.Message }
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		captured <- b.Messages
		http.Error(w, "capture only", 503)
	}))
	defer server.Close()
	for _, guidance := range []bool{true, false} {
		m.Cfg.Testing.Guidance = guidance
		m.Input.SetValue("")
		m.designTest()
		draft := m.Input.Value()
		if !strings.Contains(draft, spec.Body) || !strings.Contains(draft, prior.Selection.ID) || !strings.Contains(draft, "sessionEvidence") || strings.Contains(draft, "{ session(") {
			t.Fatalf("bad handoff %s", draft)
		}
		runner, _, err := run.New(run.Deps{Cfg: m.Cfg, Store: s, Server: server.URL, Model: "capture", MaxSteps: 1, Window: 32768}, draft)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = runner.Run(t.Context(), draft)
		if err == nil || len(captured) != 1 {
			t.Fatalf("unexpected capture/effect: %v %d", err, len(captured))
		}
		messages := <-captured
		raw, _ := json.Marshal(messages)
		text := string(raw)
		for _, required := range []string{spec.ID, spec.Body, strategy.ID, strategy.Body, prior.Selection.ID, "coverage", "unknown"} {
			if !strings.Contains(text, required) {
				t.Errorf("actual packet missing %s", required)
			}
		}
		if strings.Contains(draft, "Default testing advice caliper/standards/tests#oracle") != guidance {
			t.Error("default guidance switch ignored in actual packet")
		}
		drafts, packets := 0, 0
		for _, message := range messages {
			if message.Role == "user" && message.Content == draft {
				drafts++
			}
			if strings.HasPrefix(message.Content, "<project_knowledge>\n") {
				var packet struct{ Project string }
				if message.Role != "user" || json.Unmarshal([]byte(strings.TrimPrefix(message.Content, "<project_knowledge>\n")), &packet) != nil || packet.Project != root {
					t.Fatalf("invalid scoped host packet: %+v", message)
				}
				packets++
			}
		}
		if drafts != 1 || packets != 1 || runner.Continuation.Assignment != nil {
			t.Fatalf("human draft, host context or task authority changed: drafts=%d packets=%d assignment=%+v", drafts, packets, runner.Continuation.Assignment)
		}
	}
	// A changed suite cannot reuse a cached spec or historical freshness claim.
	m.Cfg.Testing.Suites[0].SpecVersion = "changed"
	stale := m.testDesignContext(m.Cfg.Testing.Suites[0])
	if strings.Contains(stale, spec.Body) || strings.Contains(stale, prior.Selection.ID) || !strings.Contains(stale, "not loaded") {
		t.Fatalf("stale snapshot adopted: %s", stale)
	}
}

func TestTestsSpecContextDistinguishesEmptyMissingBudgetAndHistorical(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	old, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/testing/case", "original immutable oracle", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = memory.PutKnowledge(t.Context(), s, root, "author", old.Key, "new head", old.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	suite := config.TestSuite{ID: "s", SpecKey: old.Key, SpecVersion: old.ID}
	cases := []struct {
		name, status string
		root         string
		suite        config.TestSuite
		budget       int
	}{
		{"historical", "supplied immutable version", root, suite, cfg.Tools.GraphQL.MaxResultBytes},
		{"foreign", "absent", t.TempDir(), suite, cfg.Tools.GraphQL.MaxResultBytes},
		{"omitted", "omitted-budget", root, suite, 16},
		{"mismatch", "key/version mismatch", root, config.TestSuite{ID: "s", SpecKey: "different", SpecVersion: old.ID}, cfg.Tools.GraphQL.MaxResultBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg
			c.Tools.GraphQL.MaxResultBytes = tc.budget
			got := testDesignSpecs(t.Context(), c, s, tc.root, tc.suite)["s"]
			if got.Status != tc.status {
				t.Fatalf("got %+v", got)
			}
			if tc.name == "historical" && got.Body != old.Body {
				t.Fatal("current head substituted for immutable selected spec")
			}
			if tc.name != "historical" && got.Body != "" {
				t.Fatal("unavailable data leaked")
			}
		})
	}
	empty, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/testing/empty", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	suite.SpecKey, suite.SpecVersion = empty.Key, empty.ID
	got := testDesignSpecs(t.Context(), cfg, s, root, suite)["s"]
	if got.Status != "supplied immutable version" || got.Body != "" {
		t.Fatalf("empty head confused with absence: %+v", got)
	}
}
