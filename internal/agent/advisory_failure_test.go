package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// A real HTTP deadline, canned executor completions and real Runner/tool
// receipts. The model's next request must equal a silent Continue control.
func TestProgressAdvisoryHTTPFailureKeepsExecutorContext(t *testing.T) {
	run := func(judge JudgeFunc) (*Runner, *discoveryModel, *recordSink) {
		m := &discoveryModel{}
		for i := 0; i < 3; i++ {
			m.replies = append(m.replies, llm.Reply{Message: llm.Message{Role: "assistant", ReasoningContent: fmt.Sprint("literal completed reasoning ", i), ToolCalls: []llm.ToolCall{{ID: fmt.Sprint(i), Name: "echo", Arguments: `{"text":"same"}`}}}, FinishReason: "tool_calls"})
		}
		r, calls := loopRunner(m, nil, judge)
		sink := &recordSink{}
		r.Session, r.SessionID = sink, "advice-control"
		if final, _, err := r.Run(t.Context(), "continue the requested work"); err != nil || final != "done" || len(m.seen) != 4 || len(*calls) != 3 || r.Outcome.Status != outcome.Completed {
			t.Fatalf("advisory failure interrupted executor: final=%q requests=%d effects=%d outcome=%+v err=%v", final, len(m.seen), len(*calls), r.Outcome, err)
		}
		return r, m, sink
	}
	_, control, _ := run(func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: req.Options[0], Confidence: .99}, nil
	})
	input := make(chan decision.Request, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Context, Instructions string
			Options               []string
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		input <- decision.Request{Context: body.Context, Instructions: body.Instructions, Options: body.Options}
		<-release // The advice arrives after the actual client deadline.
		json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": body.Options[0], "confidence": .42864408890925626, "model": "controlled-decision", "device": "cpu"})
	}))
	t.Cleanup(func() { close(release); server.Close() })
	_, continued, sink := run(decision.New(server.URL, "controlled-decision", decision.DeviceCPU, 50*time.Millisecond).Judge)
	if !reflect.DeepEqual(control.seen, continued.seen) {
		t.Fatal("failed advice added, removed or rewrote executor context")
	}
	wire := <-input
	inputs, failures := 0, 0
	for i, kind := range sink.kinds {
		switch kind {
		case "judgment-input":
			inputs++
			if sink.texts[i] != wire.Context || len(wire.Options) != 3 || wire.Instructions == "" {
				t.Fatal("actual failed input was not preserved")
			}
		case "judgment-error":
			failures++
			if !strings.Contains(sink.texts[i], "timed out") || !strings.Contains(sink.texts[i], server.URL) {
				t.Fatal("actual transport failure was not preserved", sink.texts[i])
			}
		case "guidance", decision.KindJudgment:
			t.Fatal("failed advice fabricated a nudge or verdict", kind)
		}
	}
	if inputs != 1 || failures != 1 {
		t.Fatalf("unexpected retry or missing evidence: inputs=%d failures=%d", inputs, failures)
	}
}

func TestProgressAdvisoryFailureMarksAttemptedPhase(t *testing.T) {
	for _, failure := range []string{"transport", "unsupported option"} {
		t.Run(failure, func(t *testing.T) {
			facts := BookkeepingFacts{Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: "task-brief", SelectedID: "current"}}
			r := &Runner{Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "attempted-phase", LoopStall: 5,
				Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: "current"}},
				Assigned:     &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
			r.Continuation.Add("intent", "user", "retain this exact user request", false)
			before := r.Continuation.Clone()
			w, attempts, failures := &watcher{advice: "previous delivered advice"}, 0, 0
			r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				attempts++
				if failure == "unsupported option" {
					return decision.Verdict{Option: "unsupported direction", Confidence: .9}, nil
				}
				return decision.Verdict{}, errLoopDown
			}
			log := func(kind, text string) error {
				if kind == "judgment-error" {
					failures++
				} else if kind != "judgment-input" {
					t.Fatal("failed advice generated other output", kind, text)
				}
				return nil
			}
			if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 0 {
				t.Fatal("initial phase unexpectedly judged", err)
			}
			facts.Checkpoint.Phase, facts.Checkpoint.PATStep = "EXECUTE", "claimed"
			w.discoveryCalls = 6
			for i := 0; i < 2; i++ {
				if advice, err := r.reviewDiscovery(t.Context(), w, log); err != nil || advice != "" {
					t.Fatalf("failed advice stopped work or nudged: %q %v", advice, err)
				}
			}
			if attempts != 1 || failures != 1 || w.discoveryCalls != 1 || w.advice != "previous delivered advice" || !reflect.DeepEqual(before, r.Continuation) {
				t.Fatalf("failed phase was retried or mutated context: attempts=%d failures=%d cadence=%d", attempts, failures, w.discoveryCalls)
			}
			// Real new phase evidence still invokes the existing advisory mechanism.
			facts.Checkpoint.Phase = "VALIDATE"
			if _, err := r.reviewDiscovery(t.Context(), w, log); err != nil || attempts != 2 {
				t.Fatalf("new phase was suppressed: attempts=%d err=%v", attempts, err)
			}
		})
	}
}

func TestProgressAdvisoryFailureKeepsAuthoritativeErrors(t *testing.T) {
	for _, failure := range []string{"cancel", "run deadline", "input write", "failure write", "encoding"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var deadlineCancel context.CancelFunc
			if failure == "run deadline" {
				ctx, deadlineCancel = context.WithTimeout(t.Context(), time.Millisecond)
				defer deadlineCancel()
			}
			saved, attempts := errors.New("actual evidence append failure"), 0
			r := &Runner{Prompts: prompts.Builtin(), Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
				attempts++
				if failure == "cancel" {
					cancel()
				}
				if failure == "run deadline" {
					<-ctx.Done()
				}
				return decision.Verdict{}, errLoopDown
			}}
			packet := progressPacket{Checkpoint: ProgressCheckpoint{Phase: "EXECUTE"}}
			if failure == "encoding" {
				packet.Facts = make(chan int)
			}
			w := &watcher{discoveryCalls: 5}
			advice, err := r.progressAdvice(ctx, w, packet, "discovery-checkpoint", "controlled advice", func(kind, _ string) error {
				if failure == "input write" && kind == "judgment-input" || failure == "failure write" && kind == "judgment-error" {
					return saved
				}
				return nil
			})
			if err == nil || advice != "" || w.phaseFacts != ([32]byte{}) || w.discoveryCalls != 5 {
				t.Fatalf("authoritative error became advisory: advice=%q error=%v watcher=%+v", advice, err, w)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) || failure == "run deadline" && !errors.Is(err, context.DeadlineExceeded) || strings.HasSuffix(failure, "write") && !errors.Is(err, saved) {
				t.Fatal("authoritative error lost its cause", err)
			}
			if (failure == "input write" || failure == "encoding") && attempts != 0 {
				t.Fatal("Judge called before durable input", attempts)
			}
		})
	}
	// PAT enforcement is advisory too: an unavailable judgment is recorded
	// and never blocks the executor. Encoding and evidence-write errors
	// above remain authoritative on both routes.
	r := &Runner{Prompts: prompts.Builtin(), Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "root"}},
		Assigned: &AssignedOps{Store: "owner", Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil }},
		Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{}, errLoopDown
		}}
	recorded := false
	if _, err := r.enforce(t.Context(), true, "complete answer", func(kind, text string) error {
		recorded = recorded || kind == "judgment-error" && strings.Contains(text, errLoopDown.Error())
		return nil
	}); err != nil || !recorded {
		t.Fatalf("advisory PAT decision failure stopped work or went unrecorded: err=%v recorded=%v", err, recorded)
	}
}
