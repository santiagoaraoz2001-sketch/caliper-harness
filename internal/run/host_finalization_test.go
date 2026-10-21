package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func TestHostConfiguredCheckPublicFactoryPreservesModelClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	srv := codingFixture(t, "structured", nil)
	cfg := config.Defaults()
	cfg.Model.Server, cfg.Model.Runtime, cfg.Decision.Server = srv.URL, "openai", srv.URL
	cfg.Recall.TopN = 0
	cfg.Auth.Mode = config.AuthAll
	cfg.Coding.CheckCommand = "printf host > host-marker"
	sink := &codingSink{dir: filepath.Join(dir, "sessions")}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	approvals := 0
	r, _, err := assignedCodingNew(t, Deps{Store: s, Cfg: cfg, Server: cfg.Model.Server, Model: "fixture", MaxSteps: 3, Window: 32768, Session: sink, SessionID: "host", AskYesNo: func(_ context.Context, p string) bool {
		approvals++
		if _, err := os.Stat("host-marker"); !os.IsNotExist(err) {
			t.Error("effect before approval")
		}
		events, err := session.Read(sink.dir, "host")
		if err != nil {
			t.Fatal(err)
		}
		announced := false
		for _, e := range events {
			announced = announced || e.Kind == "host_check" && strings.Contains(e.Text, "original model answer remains unverified")
		}
		if !announced {
			t.Error("approval before durable host announcement")
		}
		_, body, found := strings.Cut(p, "Complete validated action (JSON):\n")
		var action struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		return found && json.Unmarshal([]byte(body), &action) == nil && action.Tool == "check" && action.Args["cmd"] == cfg.Coding.CheckCommand
	}}, "false model PASS is only a claim")
	if err != nil {
		t.Fatal(err)
	}
	final, _, err := r.Run(context.Background(), "false model PASS is only a claim")
	b, readErr := os.ReadFile("host-marker")
	if err != nil || readErr != nil || string(b) != "host" || approvals != 1 || final != "PASS complete" || r.Outcome.Status != outcome.Stopped || !r.Continuation.Coding.Passed() {
		t.Fatalf("actual host check marker=%q read=%v final=%q approvals=%d err=%v outcome=%+v", b, readErr, final, approvals, err, r.Outcome)
	}
	assertQualifiedTaskWithOpenParents(t, r, s)
	events, err := session.Read(sink.dir, "host")
	if err != nil {
		t.Fatal(err)
	}
	host, rawClaim, nativeCalls := 0, false, 0
	for _, e := range events {
		if e.Kind == "host_check" {
			host++
		}
		if e.Kind == "native_reply" {
			rawClaim = rawClaim || strings.Contains(e.Text, "PASS complete")
			if strings.Contains(e.Text, "tool_calls") && !strings.Contains(e.Text, "task_start") {
				nativeCalls++
			}
		}
	}
	if host != 1 || !rawClaim || nativeCalls != 0 {
		t.Fatalf("host provenance announcement=%d rawclaim=%t invented native=%d", host, rawClaim, nativeCalls)
	}
	restored, _, err := session.Restore(events, 32768)
	if err != nil || restored.Coding.Passed() {
		t.Fatalf("restored actual receipt must be historical: %+v %v", restored.Coding, err)
	}
}

type hostFaultSink struct {
	*codingSink
	kind string
}

func (s *hostFaultSink) Append(ctx context.Context, id, kind, text string) error {
	if kind == s.kind {
		return fmt.Errorf("owned %s append failure", kind)
	}
	return s.codingSink.Append(ctx, id, kind, text)
}

// A whole-run budget can expire before the configured check is reached. Hold
// the first scripted claim on its actual Runner context instead of assuming
// real readiness/store work always leaves part of a one-second budget.
type hostBudgetClaimModel struct {
	agent.Model
}

func (m hostBudgetClaimModel) ToolProtocol() string { return "structured" }
func (m hostBudgetClaimModel) ChatTools(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	<-ctx.Done()
	return llm.Reply{}, ctx.Err()
}

func TestHostConfiguredCheckSettlementAndRevision(t *testing.T) {
	for _, scenario := range []string{"failed", "denied", "failed model check", "incomplete model check", "denied model check", "model passed", "fresh mutation", "other tool failure", "invalid model check", "steps exhausted", "announcement failure", "receipt failure", "result failure", "cancelled approval", "run budget", "unset"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			cfg.Recall.TopN, cfg.Auth.Mode = 0, config.AuthAll
			cfg.Coding.CheckCommand = "printf x >> marker"
			calls := []codingCall{}
			steps, want, markers, host, approvals := 4, outcome.Completed, "x", 1, 1
			sink := &hostFaultSink{codingSink: &codingSink{dir: filepath.Join(dir, "sessions")}}
			switch scenario {
			case "failed":
				cfg.Coding.CheckCommand += "; exit 9"
				want = outcome.Failed
			case "denied", "denied model check":
				want, markers = outcome.Stopped, ""
			case "failed model check":
				cfg.Coding.CheckCommand += "; exit 9"
				want = outcome.Failed
			case "incomplete model check":
				cfg.Coding.CheckCommand += "; sleep 10; touch late"
				want = outcome.Stopped
			case "other tool failure":
				calls, approvals = []codingCall{{"bash", map[string]any{"cmd": "exit 9"}}}, 2
			case "fresh mutation":
				calls = []codingCall{{"check", map[string]any{}}, {"bash", map[string]any{"cmd": "printf mutation > changed"}}}
				markers, approvals = "xx", 3
			case "invalid model check":
				calls = []codingCall{{"check", map[string]any{"cmd": "touch forbidden"}}}
			case "steps exhausted":
				steps, want, markers, host, approvals = 1, outcome.Stopped, "", 0, 0
			case "announcement failure":
				sink.kind = "host_check"
				want, markers, host, approvals = outcome.Failed, "", 0, 0
			case "receipt failure":
				sink.fail = true
				want, markers = outcome.Failed, ""
			case "result failure":
				sink.kind, want = "result", outcome.Failed
			case "cancelled approval":
				want, markers = outcome.Cancelled, ""
			case "run budget":
				cfg.Coding.CheckCommand = "printf x >> marker; sleep 10; printf late > late"
				want, markers, host, approvals = outcome.Stopped, "", 0, 0
			case "unset":
				cfg.Coding.CheckCommand = ""
				want, markers, host, approvals = outcome.Stopped, "", 0, 0
			}
			if strings.Contains(scenario, "model check") || scenario == "model passed" {
				if scenario != "invalid model check" {
					calls, host = []codingCall{{"check", map[string]any{}}}, 0
					if scenario == "incomplete model check" {
						calls[0].args["timeout"] = 1
					}
				}
			}
			srv := codingFixture(t, "structured", calls)
			cfg.Model.Server, cfg.Model.Runtime, cfg.Decision.Server = srv.URL, "openai", srv.URL
			owned, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer owned.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			actualApprovals := 0
			builder := func(d Deps, prompt string) (*agent.Runner, []string, error) { return assignedCodingNew(t, d, prompt) }
			if scenario == "unset" {
				builder = New
				bindDecisionFixture(t, &cfg, nil)
			}
			r, _, err := builder(Deps{Store: owned, Cfg: cfg, Server: srv.URL, Model: "fixture", MaxSteps: steps, Window: 32768, Session: sink, SessionID: "host", AskYesNo: func(context.Context, string) bool {
				actualApprovals++
				if scenario == "cancelled approval" {
					cancel()
				}
				return !strings.Contains(scenario, "denied")
			}}, "ordinary final check control")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "run budget" {
				r.MaxRunSeconds = 1
				r.Model = hostBudgetClaimModel{Model: r.Model}
			}
			final, _, runErr := r.Run(ctx, "ordinary final check control")
			b, _ := os.ReadFile("marker")
			qualified := want == outcome.Completed
			if qualified {
				want = outcome.Stopped // No scripted parent transitions.
				assertQualifiedTaskWithOpenParents(t, r, owned)
			}
			if r.Outcome.Status != want || string(b) != markers || actualApprovals != approvals || scenario != "run budget" && final != "PASS complete" {
				t.Fatalf("status=%+v want=%s marker=%q want=%q approvals=%d want=%d final=%q err=%v", r.Outcome, want, b, markers, actualApprovals, approvals, final, runErr)
			}
			if scenario == "run budget" && (!errors.Is(runErr, context.DeadlineExceeded) || !strings.Contains(r.Outcome.Reason, "max_run_seconds=1") || r.Continuation.Assignment != nil || r.Continuation.Coding.Passed()) {
				t.Fatalf("whole-run deadline did not preserve the unclaimed, unverified task: %+v coding=%+v assignment=%+v err=%v", r.Outcome, r.Continuation.Coding, r.Continuation.Assignment, runErr)
			}
			if _, err := os.Stat("late"); !os.IsNotExist(err) {
				t.Fatal("late effect survived budget")
			}
			if _, err := os.Stat("forbidden"); !os.IsNotExist(err) {
				t.Fatal("conflicting model check executed")
			}
			events, err := session.Read(sink.dir, "host")
			if err != nil {
				t.Fatal(err)
			}
			actualHost := 0
			for _, e := range events {
				if e.Kind == "host_check" {
					actualHost++
				}
			}
			if actualHost != host {
				t.Fatalf("host announcements=%d want=%d", actualHost, host)
			}
			if qualified && !r.Continuation.Coding.Passed() {
				t.Fatal("missing current check")
			}
		})
	}
}
