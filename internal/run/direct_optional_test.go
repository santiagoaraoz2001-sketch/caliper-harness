package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestOptionalPlanDirectActualWriteCheck(t *testing.T) {
	for _, proposed := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "proposed"}[proposed], func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Model.ToolProtocol = "structured"
			command := `test "$(cat direct.txt)" = actual; printf checked > marker`
			cfg.Coding.CheckCommand = command
			args := map[string]any{}
			if proposed {
				cfg.Coding.CheckCommand = ""
				args["cmd"] = command
			}
			srv := codingFixture(t, "structured", []codingCall{{"graphql", map[string]any{"query": `mutation {putKnowledge(key:"working/direct",body:"actual product fact",expectedPreviousId:""){id}}`}}, {"write", map[string]any{"path": "direct.txt", "content": "actual"}}, {"check", args}})
			s := openAssignedStore(t, dir)
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			bindDecisionFixture(t, &cfg, nil)
			r, _, err := New(Deps{Store: s, Cfg: cfg, Server: srv.URL, Model: "fixture", MaxSteps: 5, Window: 32768, Session: sink, SessionID: "direct"}, "write actual and check")
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"prepare_task", "ready_task", "task_start", "workflow_ready"} {
				if _, ok := r.Registry.Lookup(name); ok {
					t.Fatalf("ordinary view exposes %s", name)
				}
			}
			judgments := 0
			judgedPhases := []string{}
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				if len(q.Options) != 3 || q.Options[0] != r.Prompts.Text("decision.discovery.continue") {
					t.Fatalf("ordinary work requested PAT enforcement: %+v", q.Options)
				}
				var p struct {
					PlanningActive, CurrentClaim, Assigned bool
					Facts                                  json.RawMessage
					Checkpoint                             struct{ Phase, PATStep string }
				}
				if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
					t.Fatal(err)
				}
				wantPhase := "EXECUTE"
				if r.Continuation.Coding.Passed() {
					wantPhase = "VALIDATE"
				}
				judgedPhases = append(judgedPhases, p.Checkpoint.Phase)
				if p.PlanningActive || p.CurrentClaim || p.Assigned || string(p.Facts) != "null" || p.Checkpoint.Phase != wantPhase || p.Checkpoint.PATStep != "not-selected" {
					t.Fatalf("ordinary check invented PAT ownership: %+v", p)
				}
				return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
			}
			_, messages, err := r.Run(context.Background(), "write actual and check")
			if err != nil || r.Outcome.Status != outcome.Completed {
				t.Fatalf("ordinary result=%+v err=%v", r.Outcome, err)
			}
			if r.Continuation.Assignment != nil || r.Continuation.Workflow != nil {
				t.Fatal("ordinary work created PAT scope")
			}
			directRole := false
			for _, message := range messages {
				if message.Role == "system" {
					directRole = directRole || strings.Contains(message.Content, r.Prompts.Text("system.coding.direct"))
					if strings.Contains(message.Content, r.Prompts.Text("system.task.prepare")) {
						t.Fatal("ordinary system instructions require PAT preparation")
					}
				}
			}
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "guidance" {
					t.Fatal("Continue injected a model message")
				}
			}
			if judgments != 2 || !directRole || strings.Join(judgedPhases, ",") != "EXECUTE,VALIDATE" {
				t.Fatalf("ordinary phase supervision or role missing: judgments=%d directRole=%t phases=%v", judgments, directRole, judgedPhases)
			}
			entries, e := memory.Knowledge(context.Background(), s, r.Workspace, "working/direct", "", 1, 4096, true)
			if e != nil || len(entries) != 1 || entries[0].Body != "actual product fact" {
				t.Fatalf("ordinary owning knowledge missing: %+v %v", entries, e)
			}
			c := r.Continuation.Coding
			if !c.Passed() || c.AcceptanceCommand != "" || c.Receipt.Acceptance != nil {
				t.Fatalf("not a direct actual receipt: %+v", c)
			}
			b, _ := os.ReadFile("marker")
			if string(b) != "checked" {
				t.Fatal("check never executed")
			}
			events, err := session.Read(sink.dir, "direct")
			if err != nil {
				t.Fatal(err)
			}
			restored, _, err := session.Restore(events, 32768)
			if err != nil || restored.Coding.Passed() {
				t.Fatal("historical direct receipt qualified", err)
			}
		})
	}
}

func TestOptionalDirectFinalCheckAndInvalidation(t *testing.T) {
	for _, mode := range []string{"host-final", "later-edit", "check-failed", "denied"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			cfg.Recall.TopN = 0
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Model.ToolProtocol = "structured"
			cfg.Coding.CheckCommand = `test "$(cat direct.txt)" = actual; result=$?; printf checked >> marker; exit $result`
			calls := []codingCall{{"write", map[string]any{"path": "direct.txt", "content": "actual"}}}
			if mode != "host-final" {
				calls = append(calls, codingCall{"check", map[string]any{}})
			}
			if mode == "later-edit" {
				calls = append(calls, codingCall{"bash", map[string]any{"cmd": "printf changed > direct.txt"}})
			}
			if mode == "check-failed" {
				cfg.Coding.CheckCommand = "exit 7"
			}
			if mode == "denied" {
				cfg.Auth.Mode = config.AuthAll
			}
			srv := codingFixture(t, "structured", calls)
			s := openAssignedStore(t, dir)
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			bindDecisionFixture(t, &cfg, nil)
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", MaxSteps: 6, Window: 32768, Session: sink, SessionID: "direct", AskYesNo: func(context.Context, string) bool { return false }}, "write then check")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = r.Run(context.Background(), "write then check")
			if mode == "host-final" {
				if err != nil || !r.Continuation.Coding.Passed() || r.Outcome.Status != outcome.Completed {
					t.Fatal(r.Outcome, err)
				}
			} else if r.Continuation.Coding.Passed() || mode != "denied" && r.Outcome.Status == outcome.Completed {
				t.Fatal("invalid work qualified", r.Outcome)
			}
			if mode == "denied" {
				if r.Outcome.Status != outcome.Completed || !strings.Contains(r.Evidence, "correctness/test count unverified") {
					t.Fatal("denied effects were presented as verified work", r.Outcome, r.Evidence)
				}
				if _, err := os.Stat("marker"); !os.IsNotExist(err) {
					t.Fatal("denied check executed")
				}
				if _, err := os.Stat("direct.txt"); !os.IsNotExist(err) {
					t.Fatal("denied write executed")
				}
			}
			if mode == "later-edit" {
				b, _ := os.ReadFile("marker")
				if string(b) != "checkedchecked" {
					t.Fatalf("stale pass not rechecked: %q", b)
				}
			}
		})
	}
}

func TestOptionalExplicitFullPreservesAdvancedRegistry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	s := openAssignedStore(t, dir)
	r, _, e := New(Deps{Tools: "full", Cfg: cfg, Store: s, Model: "component", Server: "http://127.0.0.1:1", MaxSteps: 1, Window: 32768}, "advanced explicit mode")
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"plan", "graphql", "prepare_task", "ready_task", "task_start", "workflow_focus", "launch", "edit", "check"} {
		if _, ok := r.Registry.Lookup(n); !ok {
			t.Errorf("explicit full lost %s", n)
		}
	}
	if r.PlanningActive() {
		t.Fatal("full tool inventory created planning authority")
	}
}
