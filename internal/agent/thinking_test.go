package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

func thinkingFixture(t *testing.T, r *Runner, fields [][2]string, hold bool, log func(string, string) error) (llm.Reply, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for i, field := range fields {
			delta := map[string]any{field[0]: field[1]}
			if hold && i == 0 {
				delta["tool_calls"] = []any{map[string]any{"index": 0, "id": "partial", "type": "function", "function": map[string]string{"name": "bash", "arguments": "{\"cmd\":\""}}}
			}
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		if hold {
			<-req.Context().Done()
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, err := r.thinkingContext(ctx, log)
	if err != nil {
		return llm.Reply{}, err
	}
	def := llm.ToolDefinition{Type: "function"}
	def.Function.Name = "bash"
	def.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{"cmd": map[string]any{"type": "string"}}}
	return llm.New(srv.URL, "fixture").ChatTools(ctx, nil, []llm.ToolDefinition{def}, nil)
}
func thinkingRunner(judge JudgeFunc) *Runner {
	return &Runner{ThinkingLoop: ThinkingLoop{16, 8, 256, 4096, 8192}, Threshold: .7, Judge: judge}
}

type thinkingEvidence struct {
	Field, Unit, Sample, UnitEncoding, SampleEncoding, SampleSHA256       string
	Repeat, ObservedBytes, OmittedBytes, UnitValueBytes, SampleValueBytes int
	Claimed                                                               bool
}

func readThinkingEvidence(t *testing.T, text string) thinkingEvidence {
	t.Helper()
	metadata, values, ok := strings.Cut(text, "\nUnit:\n")
	var evidence thinkingEvidence
	if err := json.Unmarshal([]byte(metadata), &evidence); err != nil || !ok {
		t.Fatalf("invalid evidence metadata: %v %s", err, text)
	}
	if evidence.UnitValueBytes < 0 || evidence.UnitValueBytes > len(values) {
		t.Fatal("invalid unit length")
	}
	evidence.Unit = values[:evidence.UnitValueBytes]
	sample, ok := strings.CutPrefix(values[evidence.UnitValueBytes:], "\nSample:\n")
	if !ok || len(sample) != evidence.SampleValueBytes {
		t.Fatal("invalid sample length")
	}
	evidence.Sample = sample
	return evidence
}
func TestThinkingLoopHeldPartial(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict decision.Verdict
		err     error
	}{
		{"confirmed", decision.Verdict{Pick: 0, Option: loopStuck, Confidence: .9}, nil},
		{"uncertain", decision.Verdict{Pick: 1, Option: loopProgress, Confidence: .3}, nil},
		{"unavailable", decision.Verdict{}, errors.New("judge unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, logs := 0, 0
			r := thinkingRunner(func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				calls++
				if req.MaxBytes != 8192 || !strings.Contains(req.Context, "original requirement") {
					t.Fatal("complete bounded task evidence missing")
				}
				evidence := readThinkingEvidence(t, req.Context)
				if evidence.Field != "reasoning" || evidence.UnitEncoding != "utf8" || evidence.SampleEncoding != "utf8" || evidence.Repeat != 16 || evidence.Claimed || len(evidence.Unit) < 8 || evidence.ObservedBytes-len(evidence.Sample) != evidence.OmittedBytes {
					t.Fatalf("evidence %+v", evidence)
				}
				return tc.verdict, tc.err
			})
			r.Continuation.Entries = []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "original requirement"}}}
			thought := strings.Repeat("<|channel>thought", 16)
			parts := [][2]string{{"reasoning", thought[:13]}, {"reasoning", thought[13:]}}
			start := time.Now()
			reply, err := thinkingFixture(t, r, parts, true, func(k, s string) error { logs++; return nil })
			wantCause := thinkingLoopStop
			if tc.err == nil {
				wantCause = context.DeadlineExceeded
			}
			if !errors.Is(err, wantCause) || tc.err == nil && time.Since(start) < 800*time.Millisecond || calls != 1 || logs < 1 || reply.Message.Reasoning != thought || reply.ThinkingOnly || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].ID != "partial" || reply.Message.ToolCalls[0].Arguments != "{\"cmd\":\"" {
				t.Fatalf("reply=%+v err=%v calls=%d logs=%d elapsed=%s", reply, err, calls, logs, time.Since(start))
			}
		})
	}
}
func TestThinkingLoopProgressAndFieldReset(t *testing.T) {
	unit := "abcdefgh"
	calls := 0
	r := thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Pick: 1, Option: loopProgress, Confidence: .9}, nil
	})
	fields := [][2]string{{"reasoning", strings.Repeat(unit, 100)}, {"reasoning", "!" + strings.Repeat(unit, 16)}}
	reply, err := thinkingFixture(t, r, fields, false, nil)
	if err != nil || calls != 2 || reply.Message.Content != "done" || reply.Message.Reasoning != fields[0][1]+fields[1][1] {
		t.Fatalf("same-stream calls=%d reply=%+v %v", calls, reply, err)
	}
	calls = 0
	fields = [][2]string{{"reasoning", strings.Repeat(unit, 8)}, {"reasoning_content", strings.Repeat(unit, 8)}, {"reasoning", strings.Repeat(unit, 8)}}
	if _, err = thinkingFixture(t, r, fields, false, nil); err != nil || calls != 0 {
		t.Fatalf("fields merged calls=%d %v", calls, err)
	}
	calls = 0
	if _, err = thinkingFixture(t, r, [][2]string{{"reasoning", "isolated <|channel>thought; ordinary code for i := 0; i < 100; i++ { work(i) }"}}, false, nil); err != nil || calls != 0 {
		t.Fatalf("ordinary code tripped %d %v", calls, err)
	}
}
func TestThinkingLoopFailurePrecedenceAndDisable(t *testing.T) {
	ctx := context.Background()
	r := &Runner{}
	same, err := r.thinkingContext(ctx, nil)
	if err != nil || same != ctx {
		t.Fatal("disabled changed context")
	}
	saved := errors.New("durable append failed")
	r = thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
		t.Fatal("judged before durable evidence")
		return decision.Verdict{}, nil
	})
	_, err = thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, true, func(string, string) error { return saved })
	if !errors.Is(err, saved) || errors.Is(err, thinkingLoopStop) {
		t.Fatalf("recording misclassified %v", err)
	}
	// The configured byte bound belongs to the complete client envelope, before dialing.
	r = thinkingRunner(decision.New("http://unused.invalid:1", "configured-model", decision.DeviceCPU, time.Second).Judge)
	r.ThinkingLoop.MaxInputBytes = 20
	_, err = thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, true, nil)
	if !errors.Is(err, thinkingLoopStop) || !strings.Contains(err.Error(), "complete request bytes") {
		t.Fatalf("overflow %v", err)
	}
	r = thinkingRunner(nil)
	r.ThinkingLoop.SampleBytes = 1
	if _, err = r.thinkingContext(ctx, nil); err == nil {
		t.Fatal("invalid direct policy admitted")
	}
}

func TestThinkingLoopCancellationAndJudgmentLogFailure(t *testing.T) {
	pre, stop := context.WithCancel(context.Background())
	stop()
	r := thinkingRunner(nil)
	r.ThinkingLoop.SampleBytes = 1
	if _, err := r.thinkingContext(pre, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation before policy: %v", err)
	}
	unit := strings.Repeat("abcdefgh", 16)
	for _, cancelJudge := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		saved := errors.New("judgment append failed")
		r := thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
			if cancelJudge {
				cancel()
			}
			return decision.Verdict{Pick: 0, Option: loopStuck, Confidence: .9}, nil
		})
		observed, err := r.thinkingContext(ctx, func(k, s string) error {
			if k == decision.KindJudgment {
				return saved
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": unit}}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
			<-req.Context().Done()
		}))
		_, err = llm.New(srv.URL, "fixture").ChatTools(observed, nil, nil, nil)
		cancel()
		srv.Close()
		if cancelJudge {
			if !errors.Is(err, context.Canceled) || errors.Is(err, thinkingLoopStop) {
				t.Fatalf("cancel priority %v", err)
			}
		} else if !errors.Is(err, saved) || errors.Is(err, thinkingLoopStop) {
			t.Fatalf("judgment log priority %v", err)
		}
	}
}

func TestThinkingLoopSplitUTF8Sample(t *testing.T) {
	unit := "éabcdef"
	thought := "é" + strings.Repeat(unit, 16)
	calls := 0
	r := thinkingRunner(func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		calls++
		evidence := readThinkingEvidence(t, req.Context)
		sample, err := base64.StdEncoding.DecodeString(evidence.Sample)
		want := []byte(thought)[1:]
		if err != nil || evidence.UnitEncoding != "utf8" || evidence.Unit != unit || evidence.SampleEncoding != "base64" || string(sample) != string(want) || evidence.OmittedBytes != 1 || evidence.SampleSHA256 != fmt.Sprintf("%x", sha256.Sum256(want)) {
			t.Fatalf("split evidence %+v %v", evidence, err)
		}
		return decision.Verdict{Pick: 0, Option: loopStuck, Confidence: .9}, nil
	})
	r.ThinkingLoop = ThinkingLoop{16, 8, 8, 129, 8192}
	_, err := thinkingFixture(t, r, [][2]string{{"reasoning", thought}}, true, nil)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, thinkingLoopStop) || calls != 1 {
		t.Fatalf("split guard %v calls%d", err, calls)
	}
}

// Bounded exact prefix of discovery-help-native response3 content; the two
// content chunks regroup that prefix while retaining a native partial call.
func TestThinkingLoopVisibleContentHeldPartial(t *testing.T) {
	visible := "<|channel> tocó" + strings.Repeat("<|channel>", 16)
	calls, logs := 0, 0
	r := thinkingRunner(func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		calls++
		evidence := readThinkingEvidence(t, req.Context)
		if evidence.Field != "content" || req.MaxBytes != 8192 || evidence.UnitEncoding != "utf8" || !strings.Contains(evidence.Sample, "<|channel>") {
			t.Fatalf("visible repetition evidence changed: %+v", evidence)
		}
		return decision.Verdict{Pick: 1, Option: loopProgress, Confidence: .3}, nil
	})
	fields := [][2]string{{"reasoning", "\n\n\n\n\n\n"}, {"content", visible[:21]}, {"content", visible[21:]}}
	start := time.Now()
	reply, err := thinkingFixture(t, r, fields, true, func(string, string) error { logs++; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, thinkingLoopStop) || calls != 1 || logs < 1 || reply.Message.Content != visible || reply.Message.Reasoning != fields[0][1] || reply.ThinkingOnly || len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].Arguments != "{\"cmd\":\"" || time.Since(start) < 800*time.Millisecond {
		t.Fatalf("visible partial/cause changed: reply=%+v err=%v calls=%d logs=%d elapsed=%s", reply, err, calls, logs, time.Since(start))
	}
}
