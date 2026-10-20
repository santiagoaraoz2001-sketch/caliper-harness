package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func claimedAdviceRunner() *Runner {
	r := &Runner{Prompts: prompts.Builtin(), Workspace: "/project", assignedClaim: true}
	r.Outcome.Status = outcome.Unknown
	r.Continuation.Assignment = &session.Assignment{Store: "owner", Workspace: r.Workspace, PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}
	r.Continuation.Coding = &session.CodingState{TaskID: "task", Workspace: r.Workspace, Attempt: 1, Command: "project-check", AcceptanceCommand: "behavior-check"}
	r.Assigned = &AssignedOps{Facts: func(context.Context) (any, error) {
		return BookkeepingFacts{Preparation: "historical preparation recipe; not the claimed task's next action"}, nil
	}}
	r.enforcement.latest = &observed{Tool: "edit", Result: tool.Result{Text: "file edited", Changed: true}}
	return r
}

func TestClaimedBookkeepingPresentsCurrentAction(t *testing.T) {
	for _, passed := range []bool{false, true} {
		for _, pick := range []int{0, 1, 3} {
			for _, confidence := range []float64{.499, .5} {
				r := claimedAdviceRunner()
				if passed {
					c, zero := r.Continuation.Coding, 0
					c.Receipt = &session.CheckReceipt{Command: c.Command, Cwd: c.Workspace, Revision: c.Revision, Started: "started", Ended: "ended", EpisodeStarted: "started", EpisodeEnded: "ended", Status: "passed", ExitCode: &zero,
						Acceptance: &session.CommandReceipt{Command: c.AcceptanceCommand, Cwd: c.Workspace, Started: "started", Ended: "ended", Status: "passed", ExitCode: &zero}}
				}
				r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
					return decision.Verdict{Option: q.Options[pick], Confidence: confidence}, nil
				}
				advice, err := r.enforce(t.Context(), true, "actual model final", func(string, string) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				if pick == 0 || confidence < .5 {
					if advice != "" {
						t.Fatalf("silent verdict issued advice: %q", advice)
					}
					continue
				}
				key := "progress.execute"
				if passed {
					key = "progress.validate"
				}
				if !strings.Contains(r.enforcement.presentation, r.Prompts.Text(key)) || strings.Contains(advice, "request executor PAT update") || strings.Contains(advice, "historical preparation recipe") {
					t.Fatalf("claimed advice lacks current legal action: %q", advice)
				}
				if (pick == 3) != strings.Contains(r.enforcement.presentation, enforceOptions[2]) {
					t.Fatalf("knowledge advice changed: %q", advice)
				}
				if !r.assignedClaim || !r.ClaimedTaskScope(&session.WorkflowScope{Store: "owner", Kind: "task", ID: "task"}) {
					t.Fatal("advice changed claim authority")
				}
			}
		}
	}
}

func assertHistoricalRefusalDoesNotRetrigger(t *testing.T, latest *loopObservation, old unexecutedProgressCall) {
	t.Helper()
	r := claimedAdviceRunner()
	w := &watcher{recentReplies: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: old.CallID, Name: old.Tool, Arguments: `{}`}}}, {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: latest.CallID, Name: latest.Tool, Arguments: `{}`}}}}, recentReceipts: []*loopObservation{latest}}
	r.progressWatch = w
	r.enforcement.latest = &observed{CallID: latest.CallID, Tool: latest.Tool, Error: latest.Error, Result: tool.Result{Text: "actual successful result", Changed: latest.Changed, Failed: latest.Failed, Blocked: latest.Blocked, Truncated: latest.Truncated, ExitCode: latest.ExitCode}}
	r.Continuation.Entries = []session.Entry{
		{Kind: "failure", Message: llm.Message{Role: "tool", Name: old.Tool, ToolCallID: old.CallID, Content: old.Disposition}},
		{Kind: "output", Message: llm.Message{Role: "tool", Name: latest.Tool, ToolCallID: latest.CallID, Content: "actual successful result"}},
	}
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil || p.PreflightRefusal != "" || len(p.UnexecutedCalls) != 1 || p.UnexecutedCalls[0] != old {
		t.Fatalf("historical refusal lost or made current: %+v %v", p, err)
	}
	judgments := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		return decision.Verdict{Option: q.Options[1], Confidence: .9}, nil
	}
	if advice, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil || advice != "" || judgments != 0 {
		t.Fatalf("old refusal retriggered after success: advice=%q error=%v judgments=%d", advice, err, judgments)
	}
	// A fresh refusal must still reach the existing enforcer, even while the
	// latest executed receipt remains successful (whole-array rejection).
	w.recentReplies = append(w.recentReplies, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "new-refusal", Name: old.Tool, Arguments: `{}`}}})
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "failure", Message: llm.Message{Role: "tool", Name: old.Tool, ToolCallID: "new-refusal", Content: old.Disposition}})
	if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil || judgments != 1 {
		t.Fatalf("current refusal no longer triggers: %v judgments=%d", err, judgments)
	}
	if r.Continuation.Entries[0].Message.Content != old.Disposition {
		t.Fatal("historical failure evidence was rewritten")
	}
}

func TestSuccessfulClaimedWorkDoesNotRetriggerHistoricalRefusal(t *testing.T) {
	assertHistoricalRefusalDoesNotRetrigger(t, &loopObservation{CallID: "fixed", Tool: "edit", Changed: true}, unexecutedProgressCall{"old-read", "read", `entire call array refused; no calls ran: Remove unsupported argument "end_line" from read.`})
}

// Optional exact-request replay reads preserved native evidence without calling
// a model or altering the old session. The invariant above also runs in CI.
func TestCapturedBookkeepingRefusalReplay(t *testing.T) {
	dir := os.Getenv("CALIPER_BOOKKEEPING_REPLAY_DIR")
	if dir == "" {
		t.Skip("captured decision directory not supplied")
	}
	for _, name := range []string{"d1-native-00048.request.json", "d1-native-00050.request.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var q decision.Request
			var p progressPacket
			if err := json.Unmarshal(raw, &q); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
				t.Fatal(err)
			}
			if p.LatestSettled == nil || !p.CurrentClaim || p.PreflightRefusal != "" || len(p.UnexecutedCalls) != 1 {
				t.Fatal("capture is not the claimed-success/stale-refusal case")
			}
			assertHistoricalRefusalDoesNotRetrigger(t, p.LatestSettled, p.UnexecutedCalls[0])
			t.Logf("replayed latest %s %s, preserved older refusal %s", p.LatestSettled.Tool, p.LatestSettled.CallID, p.UnexecutedCalls[0].CallID)
		})
	}
}
