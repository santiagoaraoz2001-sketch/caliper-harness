package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Exact logged request from the 2026-10-20 native Laguna run: its native
// selected-client envelope is 8,499 bytes, while the configured ceiling is
// 8,192. This is transport replay with explicit HTTP doubles, not inference.
func TestThinkingAdvisoryCapturedCapacityContinues(t *testing.T) {
	raw, err := os.ReadFile("testdata/thinking-advisory-capacity-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var request decision.Request
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	client, calls, bodies := thinkingSelectedClient(t)
	client.WithInputLimit(15999)
	// No byte ceiling: the captured 8,499-byte envelope reaches the judge.
	if _, err := client.Judge(t.Context(), request); err != nil || calls.Load() != 2 {
		t.Fatalf("captured admission refused: %v HTTP=%d", err, calls.Load())
	}
	<-bodies
	attempts, failures := 0, 0
	var requests []decision.Request
	r := thinkingRunner(func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		attempts++
		requests = append(requests, req)
		return client.Judge(ctx, req)
	})
	metadata, _, _ := strings.Cut(request.Context, "\nUnit:\n")
	var facts struct {
		OriginalUserIntent string
		Assignment         *session.Assignment
		Coding             *session.CodingState
	}
	if err := json.Unmarshal([]byte(metadata), &facts); err != nil {
		t.Fatal(err)
	}
	facts.Coding.ConfigCommand = facts.Coding.Command
	r.Continuation.Assignment, r.Continuation.Coding = facts.Assignment, facts.Coding
	r.Assigned, r.assignedClaim, r.Workspace = &AssignedOps{}, true, facts.Assignment.Workspace
	r.Continuation.Add("intent", "user", facts.OriginalUserIntent, false)
	before := r.Continuation.Clone()
	evidence := readThinkingEvidence(t, request.Context)
	thought := evidence.Sample + strings.Repeat(evidence.Unit, 1000)
	reply, err := thinkingFixture(t, r, [][2]string{{"reasoning", thought}}, false, func(kind, text string) error {
		if kind == "judgment-error" {
			failures++
		} else if kind != "thinking_loop" && kind != decision.KindJudgment {
			t.Fatalf("unexpected output: %s %s", kind, text)
		}
		return nil
	})
	if err != nil || reply.Message.Content != "done" || reply.Message.Reasoning != thought || reply.FinishReason != "stop" || attempts != 1 || failures != 0 || !reflect.DeepEqual(before, r.Continuation) {
		t.Fatalf("advisory stopped/rewrote/retried: error=%v finish=%q attempts=%d failures=%d HTTP=%d", err, reply.FinishReason, attempts, failures, calls.Load())
	}
	body := <-bodies
	sent := readThinkingEvidence(t, requests[0].Context)
	if sent.Unit != evidence.Unit || !strings.Contains(thought, sent.Sample) || len(sent.Sample) < sent.Repeat*len(sent.Unit) || len(body) == 0 {
		t.Fatal("sample provenance/repetition lost")
	}
}

func TestThinkingAdvisoryFailuresContinueOnce(t *testing.T) {
	for _, mode := range []string{"unavailable", "invalid option", "invalid pick", "invalid confidence", "no judge"} {
		t.Run(mode, func(t *testing.T) {
			attempts, failures := 0, 0
			r := thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
				attempts++
				switch mode {
				case "unavailable":
					return decision.Verdict{}, errors.New("advice unavailable")
				case "invalid option":
					return decision.Verdict{Pick: 0, Option: "unsupported", Confidence: .9}, nil
				case "invalid pick":
					return decision.Verdict{Pick: -1, Option: loopStuck, Confidence: .9}, nil
				default:
					return decision.Verdict{Pick: 0, Option: loopStuck, Confidence: math.NaN()}, nil
				}
			})
			if mode == "no judge" {
				r.Judge = nil
			}
			r.Continuation = session.Continuation{}
			thought := strings.Repeat("abcdefgh", 1000)
			reply, err := thinkingFixture(t, r, [][2]string{{"reasoning", thought}}, false, func(kind, text string) error {
				if kind == "judgment-error" {
					failures++
				} else if kind != "thinking_loop" {
					t.Fatalf("failed advice became a nudge/verdict: %s", kind)
				}
				return nil
			})
			wantAttempts := 1
			if mode == "no judge" {
				wantAttempts = 0
			}
			if err != nil || reply.Message.Content != "done" || reply.Message.Reasoning != thought || attempts != wantAttempts || failures != 1 {
				t.Fatalf("advisory stopped or repeated: error=%v attempts=%d failures=%d", err, attempts, failures)
			}
		})
	}
}

func TestThinkingAdvisoryFailureRecordingRemainsAuthoritative(t *testing.T) {
	saved := errors.New("durable advice error append failed")
	r := thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{}, errors.New("unavailable")
	})
	_, err := thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, false, func(kind, text string) error {
		if kind == "judgment-error" {
			return saved
		}
		return nil
	})
	if !errors.Is(err, saved) || errors.Is(err, thinkingLoopStop) {
		t.Fatalf("recording failure lost: %v", err)
	}
}

func TestThinkingAdvisoryReductionPreservesStopCauses(t *testing.T) {
	for _, mode := range []string{"irreducible core", "cancellation", "receipt failure"} {
		t.Run(mode, func(t *testing.T) {
			attempts, receipts, failures := 0, 0, 0
			var cancel context.CancelFunc
			lastSampleBytes := 1 << 20
			r := thinkingRunner(func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				attempts++
				sample := readThinkingEvidence(t, req.Context)
				if len(sample.Sample) >= lastSampleBytes {
					t.Fatal("capacity retry did not reduce optional evidence")
				}
				lastSampleBytes = len(sample.Sample)
				if mode == "cancellation" {
					cancel()
				}
				return decision.Verdict{}, &decision.ContextCapacityError{Message: "protected core cannot fit", EncodedRequest: true}
			})
			ctx, stop := context.WithCancel(t.Context())
			cancel = stop
			defer stop()
			var prefix strings.Builder
			for n := 0; n < 1000; n++ {
				fmt.Fprintf(&prefix, "%d ", n)
			}
			thought := prefix.String() + strings.Repeat("abcdefgh", 1000)
			recordingError := errors.New("cannot save reduced advice request")
			observed, err := r.thinkingContext(ctx, func(kind, text string) error {
				if kind == "thinking_loop" {
					receipts++
					if mode == "receipt failure" && receipts == 2 {
						return recordingError
					}
				} else if kind == "judgment-error" {
					failures++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": thought}}}})
				fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", raw)
			}))
			defer server.Close()
			reply, err := llm.New(server.URL, "fixture").ChatTools(observed, nil, nil, nil)
			switch mode {
			case "irreducible core":
				if err != nil || reply.Message.Content != "done" || reply.Message.Reasoning != thought || attempts < 2 || failures != 1 || lastSampleBytes != 8*r.ThinkingLoop.Repeat {
					t.Fatalf("irreducible advice did not continue once: %v attempts=%d failures=%d sample=%d", err, attempts, failures, lastSampleBytes)
				}
			case "cancellation":
				if !errors.Is(err, context.Canceled) || attempts != 1 || receipts != 1 || failures != 0 {
					t.Fatalf("cancellation retried or lost: %v attempts=%d receipts=%d failures=%d", err, attempts, receipts, failures)
				}
			case "receipt failure":
				if !errors.Is(err, recordingError) || attempts != 1 || failures != 0 {
					t.Fatalf("unsaved reduction reached judge: %v attempts=%d failures=%d", err, attempts, failures)
				}
			}
		})
	}
}
