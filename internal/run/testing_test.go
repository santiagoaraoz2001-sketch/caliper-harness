package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/checkreport"
	"strings"
	"sync"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/graph"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

type testsArchive struct{ dir, failKind string }

func (s testsArchive) Append(ctx context.Context, id, kind, text string) error {
	if kind == s.failKind {
		return fmt.Errorf("controlled archive failure")
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

func testManagementFixture(t *testing.T) (TestDeps, config.TestSuite) {
	t.Helper()
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "source.txt"), []byte("original source"), 0600); e != nil {
		t.Fatal(e)
	}
	c := config.Defaults()
	c.Sessions.Path = t.TempDir()
	c.Decision.Disabled = true
	s := config.TestSuite{ID: "public-entry", Name: "Public entry", Purpose: "Exercise user entry", Expected: "Known public behavior", Command: "printf 'observed'", Enabled: true}
	c.Testing.Suites = []config.TestSuite{s}
	return TestDeps{Cfg: c, Workspace: root, Session: testsArchive{dir: c.Sessions.Path}, AskYesNo: func(context.Context, string) bool { return true }}, s
}

func runOptional(t *testing.T, d TestDeps, ids ...string) TestRun {
	t.Helper()
	selection, e := FreezeTests(t.Context(), d, ids)
	if e != nil {
		t.Fatal(e)
	}
	r, e := RunTests(t.Context(), d, selection)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestOptionalCheckIsolationArchiveAndFreshness(t *testing.T) {
	d, _ := testManagementFixture(t)
	d.Cfg.Coding.CheckCommand = "printf required"
	r := runOptional(t, d, "public-entry")
	if r.Status != "passed" || len(r.Results) != 1 || r.Results[0].Coverage.Status != "unknown" || r.Results[0].Receipt.Command != "printf 'observed'" {
		t.Fatalf("bad result: %+v", r)
	}
	events, e := session.Read(d.Cfg.Sessions.Path, r.Selection.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if event.Kind == "coding" || event.Kind == "continuation" || event.Kind == "host_check" {
			t.Fatalf("optional execution leaked qualification event %s", event.Kind)
		}
	}
	cont, _, e := session.Restore(events, 32768)
	if e == nil {
		t.Fatal("standalone Tests archive unexpectedly restored as a chat continuation")
	}
	if cont.Coding != nil {
		t.Fatal("optional receipt restored as coding authority")
	}
	for _, guidance := range []bool{true, false} {
		d.Cfg.Testing.Guidance = guidance
		fresh, e := TestFreshness(t.Context(), d, r)
		if e != nil || fresh != "current" {
			t.Fatalf("guidance changed evidence: %s %v", fresh, e)
		}
	}
	d.Cfg.Testing.Suites[0].Enabled = false
	d.Cfg.Testing.Suites[0].DisabledReason = "not selected"
	if fresh, _ := TestFreshness(t.Context(), d, r); fresh != "stale" {
		t.Fatal("scope change reused evidence")
	}
	d.Cfg.Testing.Suites[0].Enabled = true
	d.Cfg.Testing.Suites[0].DisabledReason = ""
	if e := os.WriteFile(filepath.Join(d.Workspace, "source.txt"), []byte("changed source"), 0600); e != nil {
		t.Fatal(e)
	}
	if fresh, _ := TestFreshness(t.Context(), d, r); fresh != "stale" {
		t.Fatal("changed source reused evidence")
	}
	after, _ := session.Read(d.Cfg.Sessions.Path, r.Selection.ID)
	beforeJSON, _ := json.Marshal(events)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("freshness changed immutable history")
	}
	history, e := ListTestRuns(t.Context(), d.Cfg.Sessions.Path, d.Workspace, 20)
	if e != nil || len(history) != 1 || history[0].Selection.Hash != r.Selection.Hash {
		t.Fatalf("history %+v %v", history, e)
	}
	if _, e := RunTests(t.Context(), d, r.Selection); e == nil {
		t.Fatal("replayed completed run accepted")
	}
	if len(TestSuites(d.Cfg)) != 2 || !TestSuites(d.Cfg)[0].Required || !TestSuites(d.Cfg)[0].Enabled {
		t.Fatal("configured obligation is not retained")
	}
}

func TestOptionalCheckRefusalsAndFailureRemainVisible(t *testing.T) {
	for _, kind := range []string{"deny", "missing", "manual", "failed", "archive", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := testManagementFixture(t)
			marker := filepath.Join(d.Workspace, "unexpected")
			d.Cfg.Testing.Suites[0].Command = "printf changed > unexpected"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "deny":
				d.AskYesNo = func(context.Context, string) bool { return false }
			case "missing":
				d.Cfg.Testing.Suites[0].Prerequisites = []config.TestPrerequisite{{Kind: "path", Value: "missing-browser"}}
			case "manual":
				d.Cfg.Testing.Suites[0].Prerequisites = []config.TestPrerequisite{{Kind: "manual", Value: "browser health unknown"}}
			case "failed":
				d.Cfg.Testing.Suites[0].Command = "exit 9"
			case "archive":
				d.Session = testsArchive{dir: d.Cfg.Sessions.Path, failKind: "test_check"}
			case "cancel":
				d.AskYesNo = func(context.Context, string) bool { cancel(); return true }
			}
			s, e := FreezeTests(ctx, d, nil)
			if e != nil {
				t.Fatal(e)
			}
			r, e := RunTests(ctx, d, s)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status == "passed" {
				t.Fatalf("%s became pass: %+v", kind, r)
			}
			if _, e := os.Stat(marker); !os.IsNotExist(e) {
				t.Fatalf("%s executed denied command", kind)
			}
			if kind == "failed" && (r.Status != "failed" || r.Results[0].Receipt.ExitCode == nil || *r.Results[0].Receipt.ExitCode != 9) {
				t.Fatalf("failure lost: %+v", r)
			}
			events, _ := session.Read(d.Cfg.Sessions.Path, r.Selection.ID)
			history, e := TestHistory(events)
			if e != nil || len(history) != 1 || history[0].Status == "passed" {
				t.Fatalf("failed history lost: %+v %v", history, e)
			}
		})
	}
}

func TestTestSelectionDeepCopyAndTamperRefusal(t *testing.T) {
	d, _ := testManagementFixture(t)
	d.Cfg.Testing.Suites[0].Prerequisites = []config.TestPrerequisite{{Kind: "path", Value: "source.txt"}}
	s, e := FreezeTests(t.Context(), d, nil)
	if e != nil {
		t.Fatal(e)
	}
	d.Cfg.Testing.Suites[0].Prerequisites[0].Value = "other"
	if s.Suites[0].Prerequisites[0].Value != "source.txt" {
		t.Fatal("selection aliases live config")
	}
	if _, e := RunTests(t.Context(), d, s); e == nil {
		t.Fatal("changed selection dispatched")
	}
	d.Cfg.Testing.Suites[0].Prerequisites[0].Value = "source.txt"
	s.Suites[0].Command = "printf tampered"
	if _, e := RunTests(t.Context(), d, s); e == nil {
		t.Fatal("tampered command dispatched")
	}
}

func TestOptionalCheckGraphSpecAndReceiptProjection(t *testing.T) {
	d, _ := testManagementFixture(t)
	s, e := store.Open(filepath.Join(t.TempDir(), "graph"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d.Store = s
	root, e := testWorkspace(d.Workspace)
	if e != nil {
		t.Fatal(e)
	}
	d.Workspace = root
	entry, e := memory.PutKnowledge(t.Context(), s, root, "authored-spec", "project/testing/strategy", "Assert the public entry point", "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	d.Cfg.Testing.Suites[0].SpecKey = entry.Key
	d.Cfg.Testing.Suites[0].SpecVersion = entry.ID
	api := graph.New(s, "v1", d.Cfg.Sessions.Path)
	d.Session = graph.NewSessionLog(api)
	r := runOptional(t, d)
	raw, e := api.Exec(t.Context(), `query($id:ID!){session(id:$id){id events{kind text}}}`, "", map[string]any{"id": r.Selection.ID})
	if e != nil || !strings.Contains(string(raw), "test_result") || !strings.Contains(string(raw), r.Selection.Hash) || !strings.Contains(string(raw), entry.ID) {
		t.Fatalf("graph receipt/spec projection: %s %v", raw, e)
	}
	_, e = memory.PutKnowledge(t.Context(), s, root, "revised-spec", entry.Key, "Different expected public behavior", entry.ID, "", "")
	if e != nil {
		t.Fatal(e)
	}
	if fresh, _ := TestFreshness(t.Context(), d, r); fresh != "stale" {
		t.Fatal("revised graph spec reused old result")
	}
	if _, e := FreezeTests(t.Context(), d, nil); e == nil {
		t.Fatal("outdated spec reference admitted")
	}
}

func TestGoJSONCoverageRequiresCurrentCompleteScope(t *testing.T) {
	valid := `{"Action":"start","Package":"p"}
{"Action":"run","Package":"p","Test":"TestBehavior"}
{"Action":"pass","Package":"p","Test":"TestBehavior"}
{"Action":"pass","Package":"p"}`
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	valid = strings.ReplaceAll(valid, `{"Action":`, `{"Time":"`+stamp+`","Action":`)
	start, end := now.Add(-time.Second).Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name, text string
		truncated  bool
		expected   []string
		status     string
	}{
		{"valid", valid, false, []string{"p/TestBehavior"}, "matched"},
		{"no-declared-inventory", valid, false, nil, "observed"},
		{"zero", `{"Time":"` + stamp + `","Action":"pass","Package":"p"}`, false, nil, "incomplete"},
		{"missing-expected", valid, false, []string{"p/TestPersistence"}, "incomplete"},
		{"skipped", strings.Replace(valid, `"Action":"pass","Package":"p","Test"`, `"Action":"skip","Package":"p","Test"`, 1), false, nil, "incomplete"},
		{"truncated", valid, true, nil, "unknown"},
		{"opaque", "PASS all assertions", false, nil, "unknown"},
		{"missing", "", false, nil, "unknown"},
		{"unfinished", strings.TrimSuffix(valid, `{"Time":"`+stamp+`","Action":"pass","Package":"p"}`), false, nil, "incomplete"},
		{"old-replay", strings.ReplaceAll(valid, stamp, now.Add(-time.Hour).Format(time.RFC3339Nano)), false, nil, "unknown"},
		{"mixed-package-skip", valid + "\n" + `{"Time":"` + stamp + `","Action":"skip","Package":"unavailable"}`, false, []string{"p/TestBehavior"}, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkreport.GoCoverage(tc.text, tc.truncated, tc.expected, start, end)
			if r.Status != tc.status {
				t.Fatalf("%+v", r)
			}
		})
	}
	d, _ := testManagementFixture(t)
	if e := os.WriteFile(filepath.Join(d.Workspace, "go.mod"), []byte("module p\n\ngo 1.24\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d.Workspace, "behavior_test.go"), []byte("package p\nimport \"testing\"\nfunc TestBehavior(t *testing.T){ if 2+2!=4 {t.Fatal(\"wrong behavior\")} }\n"), 0600); e != nil {
		t.Fatal(e)
	}
	d.Cfg.Testing.Suites[0].ReportFormat = "go-test-json"
	d.Cfg.Testing.Suites[0].ExpectedTests = []string{"p/TestBehavior"}
	d.Cfg.Testing.Suites[0].Command = "go test -json -count=1 ."
	r := runOptional(t, d)
	if r.Status != "passed" || r.Results[0].Coverage.Executed != 1 || r.Results[0].Coverage.Expected != 1 {
		t.Fatalf("actual command result %+v", r)
	}
	d.Cfg.Testing.Suites[0].Command = "go test -json -count=1 -run '^NoSuchTest$' ."
	r = runOptional(t, d)
	if r.Status != "unverified" || r.Results[0].Receipt.ExitCode == nil || *r.Results[0].Receipt.ExitCode != 0 {
		t.Fatalf("zero-match exit0 became verified %+v", r)
	}
	d.Cfg.Testing.Suites[0].Command = "go test -json -count=1 ."
	if e := os.WriteFile(filepath.Join(d.Workspace, "behavior_test.go"), []byte("package p\nimport \"testing\"\nfunc TestBehavior(t *testing.T){t.Fatal(\"known incorrect fixture\")}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	r = runOptional(t, d)
	if r.Status != "failed" || r.Results[0].Coverage.Failed != 1 {
		t.Fatalf("known wrong fixture did not fail actual command/report: %+v", r)
	}
}

func TestOptionalPersistedGuidanceAndActiveCancellation(t *testing.T) {
	t.Run("persisted-guidance-conservative-freshness", func(t *testing.T) {
		d, _ := testManagementFixture(t)
		path := filepath.Join(d.Workspace, "caliper.yaml")
		if e := config.SaveTesting(path, d.Cfg); e != nil {
			t.Fatal(e)
		}
		r := runOptional(t, d)
		d.Cfg.Testing.Guidance = false
		if e := config.SaveTesting(path, d.Cfg); e != nil {
			t.Fatal(e)
		}
		states, e := TestFreshnessBatch(t.Context(), d, []TestRun{r, r})
		if e != nil || states[0] != "stale" || states[1] != "stale" {
			t.Fatalf("saved config remains source evidence: %v %v", states, e)
		}
	})
	t.Run("active-child-cancellation", func(t *testing.T) {
		d, _ := testManagementFixture(t)
		d.Cfg.Testing.Suites[0].Command = "printf started > started.txt; sleep 30; printf forbidden > after.txt"
		s, e := FreezeTests(t.Context(), d, nil)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			deadline := time.After(5 * time.Second)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-deadline:
					cancel()
					return
				case <-tick.C:
					if _, e := os.Stat(filepath.Join(d.Workspace, "started.txt")); e == nil {
						cancel()
						return
					}
				}
			}
		}()
		r, e := RunTests(ctx, d, s)
		<-done
		if e != nil || r.Status != "cancelled" || len(r.Results) != 1 || r.Results[0].Receipt == nil {
			t.Fatalf("active cancellation lost result: %+v %v", r, e)
		}
		if _, e := os.Stat(filepath.Join(d.Workspace, "started.txt")); e != nil {
			t.Fatal("fixture never started")
		}
		if _, e := os.Stat(filepath.Join(d.Workspace, "after.txt")); !os.IsNotExist(e) {
			t.Fatal("cancelled command continued")
		}
		if fresh, e := TestFreshness(t.Context(), d, r); e != nil || fresh != "unknown" {
			t.Fatalf("cancelled incomplete fingerprint should be unknown: %s %v", fresh, e)
		}
	})
}

func TestOptionalConcurrentSelectionHasOneOwner(t *testing.T) {
	d, _ := testManagementFixture(t)
	d.Cfg.Testing.Suites[0].Command = "printf 'once\\n' >> effects.txt"
	s, e := FreezeTests(t.Context(), d, nil)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := RunTests(t.Context(), d, s); errs <- e }()
	}
	wg.Wait()
	close(errs)
	failed := 0
	for e := range errs {
		if e != nil {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("wanted one rejected duplicate, got %d", failed)
	}
	raw, e := os.ReadFile(filepath.Join(d.Workspace, "effects.txt"))
	if e != nil || string(raw) != "once\n" {
		t.Fatalf("duplicate effects %q %v", raw, e)
	}
}

func TestOptionalDisabledFailureRetainsHistoryAndRequiredCheck(t *testing.T) {
	d, _ := testManagementFixture(t)
	d.Cfg.Testing.Suites[0].Command = "exit 9"
	d.Cfg.Coding.CheckCommand = "exit 8"
	failed := runOptional(t, d, "public-entry")
	d.Cfg.Testing.Suites[0].Enabled = false
	d.Cfg.Testing.Suites[0].DisabledReason = "temporarily excluded; failure remains unresolved"
	if e := config.SaveTesting(filepath.Join(d.Workspace, "caliper.yaml"), d.Cfg); e != nil {
		t.Fatal(e)
	}
	if _, e := FreezeTests(t.Context(), d, []string{"public-entry"}); e == nil {
		t.Fatal("disabled selection ran")
	}
	required := runOptional(t, d, ConfiguredCheckID)
	if failed.Status != "failed" || required.Status != "failed" || !required.Selection.Suites[0].Required || *required.Results[0].Receipt.ExitCode != 8 {
		t.Fatalf("obligations hidden: %+v %+v", failed, required)
	}
	history, e := ListTestRuns(t.Context(), d.Cfg.Sessions.Path, d.Workspace, 20)
	if e != nil || len(history) != 2 {
		t.Fatalf("failure history lost: %+v %v", history, e)
	}
	for _, r := range history {
		if r.Status != "failed" {
			t.Fatalf("disable rewrote a failure: %+v", r)
		}
	}
}
