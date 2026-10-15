package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Native boundary stress input is synthetic, sized from the existing configured
// GraphQL result ceiling. No file effect, worker inference or check is claimed.
func largeProgressPacket(t *testing.T) (*Runner, *watcher) {
	t.Helper()
	cfg := config.Defaults()
	unit := "κ,💥,"
	dense := strings.Repeat(unit, cfg.Tools.GraphQL.MaxResultBytes/len(unit))
	r := &Runner{Prompts: prompts.Builtin(), Threshold: .7, LoopStall: 5}
	r.Continuation.Add("intent", "user", "Implement the requested browser Snake game; do not invent successful checks.", false)
	r.Continuation.Add("task", "user", "Keep the game silent and responsive.", false)
	var w watcher
	args := tool.Args{"path": "snake.js", "content": dense}
	raw, _ := json.Marshal(args)
	w.completedDiscovery(llm.Message{Role: "assistant", Content: "Unverified synthetic authored code proposal for capacity testing.", ToolCalls: []llm.ToolCall{{ID: "large-write", Name: "write", Arguments: string(raw)}}}, 5)
	r.enforcement.current = &observed{Tool: "write", Args: args, CallID: "large-write", ObservedAt: time.Now().UTC(), Result: tool.Result{Text: "Controlled write receipt preview; no actual effect claimed by this test."}}
	w.discoveryReceipt(r.enforcement.current, 5)
	w.discoveryReceipt(&observed{Tool: "help", Args: tool.Args{"topic": "graphql"}, CallID: "large-help", ObservedAt: time.Now().UTC(), Result: tool.Result{Text: dense}}, 5)
	w.source = &observed{Tool: "read", Args: tool.Args{"path": "source.js"}, CallID: "large-read", ObservedAt: time.Now().UTC(), Result: tool.Result{Text: dense}}
	w.discoveryReceipt(w.source, 5)
	w.discoveryReceipt(&observed{Tool: "ask", Args: tool.Args{"question": "Keep the game silent?"}, CallID: "actual-answer", ObservedAt: time.Now().UTC(), Result: tool.Result{Text: "Yes: silent game."}}, 5)
	r.enforcement.repair = "Current authored task update remains pending; no verification is claimed."
	return r, &w
}

func TestProgressContextCapacityReductionPreservesCoreAndOwnerRecords(t *testing.T) {
	for _, mode := range []string{"native capacity", "unrelated request refusal", "transport failure"} {
		t.Run(mode, func(t *testing.T) {
			r, w := largeProgressPacket(t)
			original, _ := json.Marshal(struct {
				Replies  []llm.Message
				Receipts []*loopObservation
			}{w.recentReplies, w.recentReceipts})
			owner, _ := json.Marshal(r.enforcement.current)
			attempts := 0
			inputs := []string{}
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				attempts++
				inputs = append(inputs, q.Context)
				if attempts == 1 {
					if dir := os.Getenv("SPECIFIC_PROGRESS_CAPTURE"); dir != "" && mode == "native capacity" {
						raw, _ := json.MarshalIndent(q, "", "  ")
						if err := os.WriteFile(filepath.Join(dir, "LARGE-REQUEST.json"), raw, 0600); err != nil {
							t.Fatal(err)
						}
					}
					switch mode {
					case "native capacity":
						return decision.Verdict{}, &decision.ContextCapacityError{Message: "controlled native admission before inference"}
					case "unrelated request refusal":
						return decision.Verdict{}, errors.New("HTTP 400: requested model not ready")
					default:
						return decision.Verdict{}, context.DeadlineExceeded
					}
				}
				return decision.Verdict{Option: q.Options[1], Confidence: .4}, nil
			}
			advice, err := r.confirmLoop(t.Context(), w, "cadence review of recorded work", func(string, string) error { return nil }, "")
			if mode == "native capacity" {
				if err != nil || attempts != 2 || advice != "" || !strings.Contains(inputs[1], "CapacityOmissions") || !strings.Contains(inputs[1], "Yes: silent game.") || !strings.Contains(inputs[1], r.enforcement.repair) || !strings.Contains(inputs[1], "responsive") {
					t.Fatalf("capacity reduction lost core or direction: attempts=%d err=%v", attempts, err)
				}
			} else if err != nil || attempts != 1 || advice != "" {
				t.Fatal("failed advisory was retried, stopped work or produced a nudge", mode)
			}
			after, _ := json.Marshal(struct {
				Replies  []llm.Message
				Receipts []*loopObservation
			}{w.recentReplies, w.recentReceipts})
			afterOwner, _ := json.Marshal(r.enforcement.current)
			// Advice may change; observations/claims may never be edited by D1-only fitting.
			var a, b map[string]any
			json.Unmarshal(original, &a)
			json.Unmarshal(after, &b)
			delete(a, "advice")
			delete(b, "advice")
			ar, _ := json.Marshal(a)
			br, _ := json.Marshal(b)
			if string(ar) != string(br) || string(owner) != string(afterOwner) {
				t.Fatal("decision fitting mutated owning observations")
			}
		})
	}
}

func TestDistinctQuestionsWithIdenticalAnswersAreProgress(t *testing.T) {
	r := &Runner{Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil }}}
	r.enforcement.repair = "PAT update pending"
	r.bookkeepingProgress(t.Context(), true)
	for _, question := range []string{"Dark theme?", "Silent game?"} {
		r.enforcement.current = &observed{Tool: "ask", Args: tool.Args{"question": question}, Result: tool.Result{Text: "Yes"}}
		if !r.bookkeepingProgress(t.Context(), true) {
			t.Fatal("distinct answer erased", question)
		}
		if r.bookkeepingProgress(t.Context(), true) {
			t.Fatal("unchanged question/answer credited twice")
		}
	}
}

func TestProgressWindowDisclosesOmittedReceiptsAndNoHistoricalTimestamp(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin()}
	var w watcher
	for i := 0; i < 6; i++ {
		w.completedDiscovery(llm.Message{Role: "assistant", Content: "recorded claim"}, 5)
		w.discoveryReceipt(&observed{Tool: "ask", Result: tool.Result{Text: "recorded answer"}}, 5)
	}
	p, err := r.progressEvidence(t.Context(), &w)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if p.OmittedReceipts != 1 || p.OmittedCompletedReplies != 1 || strings.Contains(string(raw), "0001-01-01") {
		t.Fatalf("omission/provenance misleading: %s", raw)
	}
}

// Opt-in native D1 Mac GPU control. Root owns services and explicitly supplies
// its URL; ordinary source gates never start/download models or infer.
func TestProgressNativeCapacityRoundTrip(t *testing.T) {
	url := os.Getenv("SPECIFIC_PROGRESS_NATIVE_URL")
	if url == "" {
		t.Skip("explicit Mac GPU D1 capacity control; source suite remains hermetic")
	}
	dir := os.Getenv("SPECIFIC_PROGRESS_NATIVE_CAPTURE")
	if dir == "" {
		t.Fatal("native control requires an explicit evidence folder")
	}
	cfg := config.Defaults()
	r, w := largeProgressPacket(t)
	c := decision.New(url, "LiquidAI/d1-3B", decision.DeviceGPU, time.Duration(cfg.Decision.TimeoutSeconds)*time.Second).WithPrompts(r.Prompts)
	health, err := c.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !health.IsReady() || health.Active == nil || !strings.HasPrefix(health.Active.NativeDevice, "mps:") || health.Active.ContextLimit == nil {
		t.Fatal("native control requires ready Mac MPS identity and actual reported context capacity")
	}
	c.WithSelection(health.Active.Selection)
	raw, _ := json.MarshalIndent(health, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "HEALTH.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	var last decision.Request
	var actual decision.Verdict
	r.Judge = func(ctx context.Context, q decision.Request) (decision.Verdict, error) {
		attempts++
		last = q
		raw, _ := json.MarshalIndent(q, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("REQUEST-%02d.json", attempts)), raw, 0600); err != nil {
			return decision.Verdict{}, err
		}
		actual, err = c.Judge(ctx, q)
		out := struct {
			Verdict decision.Verdict
			Error   string
		}{Verdict: actual}
		if err != nil {
			out.Error = err.Error()
		}
		raw, _ = json.MarshalIndent(out, "", "  ")
		if saveErr := os.WriteFile(filepath.Join(dir, fmt.Sprintf("RESULT-%02d.json", attempts)), raw, 0600); saveErr != nil {
			return decision.Verdict{}, saveErr
		}
		return actual, err
	}
	advice, err := r.confirmLoop(t.Context(), w, "cadence review of synthetic boundary evidence; inspect actual core and omissions", func(kind, text string) error {
		file, err := os.OpenFile(filepath.Join(dir, "EVENTS.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		return json.NewEncoder(file).Encode(struct {
			Time       time.Time
			Kind, Text string
		}{time.Now().UTC(), kind, text})
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if attempts < 2 || !strings.Contains(last.Context, "CapacityOmissions") || !strings.Contains(last.Context, "Yes: silent game.") || !strings.Contains(last.Context, "responsive") || !strings.Contains(last.Context, r.enforcement.repair) {
		t.Fatal("actual native fitting lost core evidence or failed to continue")
	}
	wantNudge := actual.Confidence >= .5 && actual.Option != r.Prompts.Text("decision.discovery.continue")
	if (advice != "") != wantNudge {
		t.Fatal("native advisory confidence/Continue silence was not respected")
	}
	raw, _ = json.MarshalIndent(struct {
		Attempts     int
		FinalRequest decision.Request
		Verdict      decision.Verdict
		Advice       string
		Scope        string
	}{attempts, last, actual, advice, "Native D1 capacity/advisory control only; no worker coding, game effect or verification claimed."}, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "SUMMARY.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProgressFittingPreservesRealFailureAcrossUnexecutedTail(t *testing.T) {
	r, w := largeProgressPacket(t)
	code := 1
	args := tool.Args{"cmd": "printf ACTUAL_BASH_FAILURE; exit 1", "timeout": int64(1)}
	_, runErr := r.assignmentExec(Direct)(t.Context(), bash.New(config.Defaults().Tools.Bash), args)
	if runErr != nil {
		t.Fatal(runErr)
	}
	note := r.enforcement.current
	note.CallID = "actual-failure"
	if !note.Result.Failed || note.Result.ExitCode == nil || *note.Result.ExitCode != code {
		t.Fatal("actual Bash control did not fail")
	}
	w.discoveryReceipt(note, 5)
	r.enforcement.current = nil // Same actual native tail boundary after a failed call.
	w.completedDiscovery(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual-failure", Name: "bash", Arguments: `{"cmd":"printf ACTUAL_BASH_FAILURE; exit 1"}`}, {ID: "unstarted", Name: "write", Arguments: `{"path":"must-not-run"}`}}}, 5)
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "failure", Unread: true, Message: llm.Message{Role: "tool", Name: "write", ToolCallID: "unstarted", Content: "call not executed: preceding call actual-failure did not complete successfully"}})
	packet, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Latest != nil || packet.LatestSettled == nil || !packet.LatestSettled.Failed || len(packet.UnexecutedCalls) != 1 || packet.UnexecutedCalls[0].CallID != "unstarted" {
		t.Fatal("failed/tail provenance lost")
	}
	for packet.reduceForCapacity() {
	}
	raw, _ := json.Marshal(packet)
	if !strings.Contains(string(raw), "ACTUAL_BASH_FAILURE") || !strings.Contains(string(raw), "actual-failure") || !strings.Contains(string(raw), "unstarted") {
		t.Fatal("latest actual failed result vanished during fitting")
	}
}
