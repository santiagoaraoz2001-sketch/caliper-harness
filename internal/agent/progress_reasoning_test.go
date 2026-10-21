package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
)

type reasoningEvidenceWire struct {
	Field, SHA256, Provenance, VerbatimSuffix string
	Bytes, OmittedBytes                       int
}

func TestCompletedProgressReasoningProjectionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text   string
		budget int
		want   string
	}{
		{"short thought", 4096, "short thought"},
		{"prefixα😀", 5, "😀"},
		{"prefixα😀", 3, ""},
		{"unchanged", 0, ""},
		{"unchanged", -1, ""},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.budget, tc.text), func(t *testing.T) {
			message := llm.Message{Role: "assistant", Content: "visible answer", ReasoningContent: tc.text, ToolCalls: []llm.ToolCall{{ID: "one", Name: "read", Arguments: `{"path":"source.go"}`}}}
			before, _ := json.Marshal(message)
			copy, evidence := completedProgressReply(message, tc.budget)
			if len(evidence) != 1 || evidence[0].VerbatimSuffix != tc.want || evidence[0].OmittedBytes != len(tc.text)-len(tc.want) || copy.Content != message.Content || copy.ReasoningContent != "" || len(copy.ToolCalls) != 1 || copy.ToolCalls[0] != message.ToolCalls[0] {
				t.Fatalf("projection changed content/calls or misrepresented UTF-8 boundary: %+v %+v", copy, evidence)
			}
			after, _ := json.Marshal(message)
			if string(before) != string(after) {
				t.Fatal("projection mutated original message")
			}
		})
	}
}

func TestCompletedProgressReasoningCapacityDoesNotMutateWindow(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin()}
	var w watcher
	message := llm.Message{Role: "assistant", ReasoningContent: strings.Repeat("exact recorded thinking ", 1000)}
	w.completedDiscovery(message, 5, 4096)
	before, _ := json.Marshal(w.recentDeliberation)
	p, err := r.progressEvidence(t.Context(), &w)
	if err != nil {
		t.Fatal(err)
	}
	if !p.reduceForCapacity() || p.OmittedDeliberation[0].VerbatimSuffix != "" {
		t.Fatal("large optional sample did not reduce")
	}
	after, _ := json.Marshal(w.recentDeliberation)
	if string(before) != string(after) || w.recentDeliberation[0].VerbatimSuffix == "" {
		t.Fatal("capacity projection mutated future watcher evidence")
	}
}

func TestCompletedProgressReasoningPreservesCancellationAndRecordingFailures(t *testing.T) {
	for _, mode := range []string{"cancel", "input-write", "judgment-write"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &Runner{Prompts: prompts.Builtin()}
			var w watcher
			w.completedDiscovery(llm.Message{Role: "assistant", Reasoning: strings.Repeat("recorded thought ", 500)}, 5, 4096)
			cause := errors.New("durable evidence write failed")
			calls := 0
			r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				calls++
				if mode == "cancel" {
					cancel()
					return decision.Verdict{}, context.Canceled
				}
				return decision.Verdict{}, errors.New("optional advice unavailable")
			}
			advice, err := r.confirmLoop(ctx, &w, "existing cadence", func(kind, _ string) error {
				if mode == "input-write" && kind == "judgment-input" || mode == "judgment-write" && kind == "judgment-error" {
					return cause
				}
				return nil
			}, "")
			want := cause
			if mode == "cancel" {
				want = context.Canceled
			}
			if advice != "" || !errors.Is(err, want) || mode == "input-write" && calls != 0 || mode != "input-write" && calls != 1 {
				t.Fatalf("authoritative failure swallowed: advice=%q err=%v calls=%d", advice, err, calls)
			}
		})
	}
}

func completedReasoningRunner(t *testing.T, message llm.Message, budget int, judge JudgeFunc) (*Runner, *discoveryModel) {
	t.Helper()
	m := &discoveryModel{}
	for i := 0; i < 5; i++ {
		reply := discoveryReply(fmt.Sprint(i))
		if i == 4 {
			reply.Message.Thinking, reply.Message.Reasoning, reply.Message.ReasoningContent = message.Thinking, message.Reasoning, message.ReasoningContent
		}
		m.replies = append(m.replies, reply)
	}
	r := discoveryRunner(t, m, judge, &recordSink{})
	r.ThinkingLoop.SampleBytes = budget
	return r, m
}

// Exact complete reasoning from captured native responses 38/39, not invented
// deliberation. This replays the real Runner/packet path with a scripted model;
// it does not re-run inference or claim that D1's decision improves.
func TestCompletedProgressReasoningCapturedSuffix(t *testing.T) {
	raw, err := os.ReadFile("testdata/pat-closure-reasoning.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded []struct{ Source, Reasoning string }
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	budget := config.Defaults().Agent.ThinkingLoop.SampleBytes
	for _, capture := range recorded {
		for _, field := range []string{"thinking", "reasoning", "reasoning_content"} {
			t.Run(capture.Source+"/"+field, func(t *testing.T) {
				message := llm.Message{Role: "assistant"}
				switch field {
				case "thinking":
					message.Thinking = capture.Reasoning
				case "reasoning":
					message.Reasoning = capture.Reasoning
				default:
					message.ReasoningContent = capture.Reasoning
				}
				judgments := 0
				r, m := completedReasoningRunner(t, message, budget, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
					judgments++
					var packet struct {
						OmittedDeliberation    []reasoningEvidenceWire
						RecentCompletedReplies []llm.Message
						OriginalUserIntent     string
					}
					if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
						t.Fatal(err)
					}
					if len(packet.OmittedDeliberation) != 1 {
						t.Fatalf("missing completed reasoning provenance: %+v", packet.OmittedDeliberation)
					}
					e := packet.OmittedDeliberation[0]
					if e.Field != field || e.VerbatimSuffix == "" || len(e.VerbatimSuffix) > budget || !utf8.ValidString(e.VerbatimSuffix) || !strings.HasSuffix(capture.Reasoning, e.VerbatimSuffix) || e.Bytes != len(capture.Reasoning) || e.OmittedBytes != len(capture.Reasoning)-len(e.VerbatimSuffix) || e.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(capture.Reasoning))) || !strings.Contains(e.Provenance, "unverified completed model deliberation") {
						t.Fatalf("missing/misrepresented actual reasoning sample: %+v", e)
					}
					if packet.OriginalUserIntent != "Preserve current user intent" {
						t.Fatal("sample displaced user intent")
					}
					for _, reply := range packet.RecentCompletedReplies {
						if reply.Thinking != "" || reply.Reasoning != "" || reply.ReasoningContent != "" {
							t.Fatal("reasoning duplicated as ordinary assistant content in judge projection")
						}
					}
					return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
				})
				if _, _, err := r.Run(t.Context(), "Preserve current user intent"); err != nil || judgments != 1 {
					t.Fatalf("existing progress touchpoint changed: judgments=%d err=%v", judgments, err)
				}
				found := false
				for _, reply := range m.seen[len(m.seen)-1] {
					found = found || reply.Thinking == capture.Reasoning || reply.Reasoning == capture.Reasoning || reply.ReasoningContent == capture.Reasoning
				}
				if !found {
					t.Fatal("judge projection erased full reasoning from next actual worker request")
				}
			})
		}
	}
}

func TestCompletedProgressReasoningBudgetAdmissionAndSilence(t *testing.T) {
	for _, mode := range []string{"continue", "uncertain", "unavailable", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			message := llm.Message{Thinking: "older representation", Reasoning: "other representation", ReasoningContent: strings.Repeat("α😀 understood fix; ", 600)}
			budget, calls := 4096, 0
			r, m := completedReasoningRunner(t, message, budget, func(_ context.Context, req decision.Request) (decision.Verdict, error) {
				calls++
				var packet struct {
					OmittedDeliberation []reasoningEvidenceWire
					CapacityOmissions   []progressOmission
				}
				if err := json.Unmarshal([]byte(req.Context), &packet); err != nil {
					t.Fatal(err)
				}
				bytes := 0
				for _, e := range packet.OmittedDeliberation {
					bytes += len(e.VerbatimSuffix)
					if !utf8.ValidString(e.VerbatimSuffix) || e.OmittedBytes != e.Bytes-len(e.VerbatimSuffix) {
						t.Fatalf("invalid suffix/omission accounting: %+v", e)
					}
				}
				if calls == 1 && (bytes == 0 || bytes > budget) {
					t.Fatalf("aggregate sample exceeded/ignored existing budget: bytes=%d", bytes)
				}
				if mode == "capacity" && calls == 1 {
					return decision.Verdict{}, &decision.ContextCapacityError{Message: "controlled existing input admission"}
				}
				if mode == "capacity" && (bytes != 0 || len(packet.CapacityOmissions) == 0) {
					t.Fatal("optional sample did not shrink with explicit admission disclosure")
				}
				if mode == "unavailable" {
					return decision.Verdict{}, errors.New("recorded decision unavailability")
				}
				v := decision.Verdict{Option: req.Options[0], Confidence: .99}
				if mode == "uncertain" {
					v.Option, v.Confidence = req.Options[1], .499
				}
				return v, nil
			})
			if _, _, err := r.Run(t.Context(), "continue work unchanged"); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "capacity" {
				want = 2
			}
			if calls != want || len(m.seen) != 6 {
				t.Fatalf("changed existing cadence/model continuation: judges=%d requests=%d", calls, len(m.seen))
			}
			for _, e := range r.Continuation.Entries {
				if e.Kind == "guidance" {
					t.Fatal("silent/failed advice injected a message")
				}
			}
			retained := false
			for _, e := range r.Continuation.Entries {
				retained = retained || e.Message.Thinking == message.Thinking && e.Message.Reasoning == message.Reasoning && e.Message.ReasoningContent == message.ReasoningContent
			}
			if !retained {
				t.Fatal("optional sample processing changed original reasoning")
			}
		})
	}
}
