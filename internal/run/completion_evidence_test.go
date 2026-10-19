package run

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"strconv"
	"strings"
	"testing"
)

func completionHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// Scripted native HTTP fixture; actual registered file publication and Bash checks.
func TestCompletionEvidencePublicRun(t *testing.T) {
	for _, scenario := range []string{"reverted", "retained", "empty create", "no edit", "refused", "opaque", "overflow", "evidence failure"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if e := os.WriteFile("owned", []byte("original"), 0600); e != nil {
				t.Fatal(e)
			}
			cfg := config.Defaults()
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Model.ToolProtocol = "structured"
			cfg.Coding.CheckCommand = "test \"$(cat owned)\" = original"
			calls := []codingCall{}
			expected := []string{"publications=0", "net=0", "current=true", strconv.Quote(cfg.Coding.CheckCommand), "correctness unverified", "not inventoried"}
			switch scenario {
			case "reverted":
				calls = []codingCall{{"read", map[string]any{"path": "owned"}}, {"edit", map[string]any{"path": "./owned", "old": "original", "new": "broken", "expected_hash": completionHash("original")}}, {"check", map[string]any{}}, {"edit", map[string]any{"path": "owned", "old": "broken", "new": "original", "expected_hash": completionHash("broken")}}}
				expected[0] = "publications=2"
				expected = append(expected, "\"owned\":reverted")
			case "retained":
				calls = []codingCall{{"edit", map[string]any{"path": "owned", "old": "original", "new": "retained", "expected_hash": completionHash("original")}}}
				cfg.Coding.CheckCommand = "test \"$(cat owned)\" = retained"
				expected[0] = "publications=1"
				expected[1] = "net=1"
				expected[3] = strconv.Quote(cfg.Coding.CheckCommand)
				expected = append(expected, "\"owned\":changed")
			case "empty create":
				calls = []codingCall{{"write", map[string]any{"path": "empty", "content": ""}}}
				expected[0] = "publications=1"
				expected[1] = "net=1"
				expected = append(expected, "\"empty\":created")
			case "refused":
				calls = []codingCall{{"edit", map[string]any{"path": "owned", "old": "absent", "new": "broken", "expected_hash": completionHash("original")}}}
				expected = append(expected, "partial=true")
			case "opaque":
				calls = []codingCall{{"bash", map[string]any{"cmd": "printf outside > opaque", "timeout": 2}}}
			case "overflow":
				for i := 0; i < 9; i++ {
					calls = append(calls, codingCall{"write", map[string]any{"path": fmt.Sprintf("p%d", i), "content": ""}})
				}
				calls = append(calls, codingCall{"write", map[string]any{"path": "p0", "content": "updated", "expected_hash": completionHash("")}})
				expected[0] = "publications=10"
				expected[1] = "net=8"
				expected = append(expected, "partial=true", "tracked=8", "\"p0\":created")
			}
			calls = append(calls, codingCall{"check", map[string]any{}})
			srv := codingFixture(t, "structured", calls)
			cfg.Model.Server = srv.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			owned, e := store.Open(filepath.Join(t.TempDir(), "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer owned.Close()
			var log agent.SessionLog = sink
			if scenario == "evidence failure" {
				log = &completionFailSink{sink}
			}
			runner, _, e := assignedCodingNew(t, Deps{Store: owned, Cfg: cfg, Server: srv.URL, Model: "fixture", MaxSteps: len(calls) + 2, Window: 32768, Session: log, SessionID: "completion"}, "fix Fetch")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = runner.Run(context.Background(), "fix Fetch")
			if scenario == "evidence failure" {
				if e == nil || runner.Outcome.Status != outcome.Failed {
					t.Fatalf("evidence failure hidden %+v %v", runner.Outcome, e)
				}
				return
			}
			if e != nil || !runner.Continuation.Coding.Passed() {
				t.Fatalf("D14 outcome changed: %+v %v", runner.Outcome, e)
			}
			assertQualifiedTaskWithOpenParents(t, runner, owned)
			t.Logf("%s actual outcome=%s evidence=%s", scenario, runner.Outcome.Status, runner.Evidence)
			for _, want := range expected {
				if !strings.Contains(runner.Evidence, want) {
					t.Errorf("missing %q in %s", want, runner.Evidence)
				}
			}
			if scenario == "reverted" {
				b, _ := os.ReadFile("owned")
				if string(b) != "original" {
					t.Fatalf("revert %q", b)
				}
			}
			events, e := session.Read(sink.dir, "completion")
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "overflow" && strings.Index(runner.Evidence, `"p0":created`) >= strings.Index(runner.Evidence, `"p1":created`) {
				t.Fatal("path display order drift")
			}
			if scenario == "reverted" {
				seen1, seen0 := false, false
				for _, ev := range events {
					if ev.Kind == "coding" {
						seen1 = seen1 || strings.Contains(ev.Text, `"exit_code":1`)
						seen0 = seen0 || strings.Contains(ev.Text, `"exit_code":0`)
					}
				}
				if !seen1 || !seen0 {
					t.Fatal("actual failed/pass check receipts missing")
				}
			}
			found := false
			for _, ev := range events {
				if ev.Kind == "evidence" && ev.Text == runner.Evidence {
					found = true
				}
			}
			if !found {
				t.Fatal("human evidence not recorded")
			}
			restored, _, e := session.Restore(events, 32768)
			if e != nil {
				t.Fatal(e)
			}
			found = false
			for _, entry := range restored.Entries {
				if entry.Kind == "evidence" && entry.Message.Content == runner.Evidence {
					found = true
				}
			}
			if !found {
				t.Fatal("bounded continuation lost evidence")
			}
			if scenario == "reverted" {
				// Fresh native conversation: fixture call IDs intentionally start at zero.
				runner.Continuation = session.Continuation{}
				runner.SessionID = "completion-reuse"
				again := codingFixture(t, "structured", []codingCall{{"check", map[string]any{}}})
				task := assignedFixtureTask(t, owned, cfg.Coding.CheckCommand)
				runner.MaxSteps++
				runner.Model = &claimFixtureModel{Model: llm.New(again.URL, "fixture").WithRuntime(cfg.Model.Runtime).WithToolProtocol("structured"), task: task.ID, pending: true, protocol: "structured"}
				_, _, e = runner.Run(context.Background(), "check only")
				if e != nil || !strings.Contains(runner.Evidence, "publications=0") {
					t.Fatalf("Runner reuse retained prior effects: %s %v", runner.Evidence, e)
				}
			}
		})
	}
}

type completionFailSink struct{ *codingSink }

func (s *completionFailSink) Append(ctx context.Context, id, kind, text string) error {
	if kind == "evidence" {
		return fmt.Errorf("controlled evidence recording failure")
	}
	return s.codingSink.Append(ctx, id, kind, text)
}
