package agent_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// The transport tests exercise actual HTTP framing. This boundary test checks
// the real runner, registry and filesystem across physical response segments.
type rawContinuationModel struct {
	t                 *testing.T
	requests          [][]llm.Message
	partial, complete string
	cancel            context.CancelFunc
	single            bool
}

func (m *rawContinuationModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	m.t.Fatal("raw tool response entered plain chat")
	return "", "", llm.Usage{}, nil
}

func (m *rawContinuationModel) ChatTools(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.requests = append(m.requests, append([]llm.Message(nil), messages...))
	request := len(m.requests)
	if m.single {
		request++
	}
	switch request {
	case 1:
		if m.cancel != nil {
			m.cancel()
		}
		prefix := &llm.ResponsePrefix{
			Strategy: "rulith_raw", Prompt: "original rendered prompt", Raw: m.partial,
			PromptSHA256:  fmt.Sprintf("%x", sha256.Sum256([]byte("original rendered prompt"))),
			RequestSHA256: strings.Repeat("b", 64), InitialState: "normal", GeneratedTokens: 7,
		}
		return llm.Reply{Message: llm.Message{Role: "assistant"}, RawNative: m.partial, NativeInitialState: "normal", Prefix: prefix, OutputLimit: true, FinishReason: "length", Usage: llm.Usage{In: 100, Out: 7, Reported: true}}, fmt.Errorf("physical output limit")
	case 2:
		if !reflect.DeepEqual(messages, m.requests[0]) {
			m.t.Error("continuation added or changed conversation messages")
		}
		return llm.Reply{RawNative: m.complete, NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 107, Out: 11, Reported: true}}, nil
	case 3:
		return llm.Reply{RawNative: "done", NativeInitialState: "normal", FinishReason: "stop", Usage: llm.Usage{In: 130, Out: 2, Reported: true}}, nil
	default:
		m.t.Fatal("unexpected repeated logical work")
		return llm.Reply{}, fmt.Errorf("unexpected request")
	}
}

// Reconstruct only complete full-response archives, excluding earlier partial
// prefixes. Tests inspect this receipt inside Execute, not merely after Run.
func completedRawReplies(t *testing.T, sink *nativeRecordingSink) []llm.Reply {
	t.Helper()
	var replies []llm.Reply
	var raw strings.Builder
	collecting, size := false, 0
	for _, e := range sink.events {
		if e.kind != "reasoning" {
			continue
		}
		if !strings.HasPrefix(e.text, "offset=") {
			var header struct {
				Bytes   int
				RawOnly *bool
			}
			if err := json.Unmarshal([]byte(e.text), &header); err != nil {
				t.Fatal(err)
			}
			collecting, size = header.RawOnly != nil, header.Bytes
			raw.Reset()
			continue
		}
		if !collecting {
			continue
		}
		header, fragment, ok := strings.Cut(e.text, "\n")
		var offset, total int
		if _, err := fmt.Sscanf(header, "offset=%d total=%d", &offset, &total); err != nil || !ok || offset != raw.Len() || total != size {
			t.Fatalf("invalid completed fragment: %q", header)
		}
		raw.WriteString(fragment)
		if raw.Len() == size {
			var reply llm.Reply
			if err := json.Unmarshal([]byte(raw.String()), &reply); err != nil {
				t.Fatal(err)
			}
			replies = append(replies, reply)
			collecting = false
		}
	}
	return replies
}

func TestRawCompletedToolArchiveBeforeEffect(t *testing.T) {
	for _, single := range []bool{true, false} {
		t.Run(fmt.Sprintf("single=%t", single), func(t *testing.T) {
			value := strings.Repeat("α🚚 raw\n", 4000) + "final suffix preserved"
			full := "<tool_call><function=record><parameter=value>\n" + value + "\n</parameter></function></tool_call>"
			model := &rawContinuationModel{t: t, single: single, partial: full[:100], complete: full}
			sink := &nativeRecordingSink{}
			effects := 0
			reg := tool.NewRegistry()
			reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
				completed := completedRawReplies(t, sink)
				if len(completed) != 1 || completed[0].RawNative != full || len(completed[0].Message.ToolCalls) != 1 {
					t.Fatalf("full final raw not durable before effect: replies=%d", len(completed))
				}
				effects++
				return tool.Result{Text: "recorded"}, nil
			}})
			r := &agent.Runner{Model: model, Registry: reg, Session: sink, SessionID: "raw-complete", MaxSteps: 2, MaxTokens: 262144}
			_, _, err := r.Run(t.Context(), "record once")
			if err != nil || effects != 1 || r.Outcome.Status != outcome.Completed {
				t.Fatalf("completion: effects=%d outcome=%+v err=%v", effects, r.Outcome, err)
			}
			toolEntries := 0
			for _, e := range sink.events {
				if e.kind == "native_reply" {
					var entry session.Entry
					if err := json.Unmarshal([]byte(e.text), &entry); err != nil {
						t.Fatal(err)
					}
					if len(entry.Message.ToolCalls) > 0 {
						toolEntries++
					}
				}
			}
			if toolEntries != 1 {
				t.Fatalf("duplicated tool transcript entry: %d", toolEntries)
			}
		})
	}
}

type failCompletedRawSink struct {
	nativeRecordingSink
	mode      string
	completed bool
}

func (s *failCompletedRawSink) Append(ctx context.Context, id, kind, text string) error {
	if kind == "reasoning" && !strings.HasPrefix(text, "offset=") {
		var header struct{ RawOnly *bool }
		if json.Unmarshal([]byte(text), &header) == nil {
			s.completed = header.RawOnly != nil
		}
		if s.completed && s.mode == "header" {
			return fmt.Errorf("controlled completed raw header failure")
		}
	}
	if kind == "reasoning" && s.completed && s.mode == "fragment" && strings.HasPrefix(text, "offset=") {
		return fmt.Errorf("controlled completed raw fragment failure")
	}
	return s.nativeRecordingSink.Append(ctx, id, kind, text)
}

func TestRawCompletedArchiveFailurePreventsEffects(t *testing.T) {
	for _, single := range []bool{true, false} {
		for _, mode := range []string{"header", "fragment"} {
			t.Run(fmt.Sprintf("single=%t/%s", single, mode), func(t *testing.T) {
				full := "<tool_call><function=record><parameter=value>exact</parameter></function></tool_call>"
				model := &rawContinuationModel{t: t, single: single, partial: full[:30], complete: full}
				sink := &failCompletedRawSink{mode: mode}
				reg := tool.NewRegistry()
				reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
					t.Fatal("effect ran without completed raw archive")
					return tool.Result{}, nil
				}})
				r := &agent.Runner{Model: model, Registry: reg, Session: sink, SessionID: "failed-raw-complete", MaxSteps: 2, MaxTokens: 262144}
				_, _, err := r.Run(t.Context(), "record once")
				if err == nil || r.Outcome.Status != outcome.Failed {
					t.Fatalf("missing failure: %+v %v", r.Outcome, err)
				}
				for _, e := range sink.events {
					if e.kind == "native_result" || e.kind == "tool" {
						t.Fatalf("failed archive admitted execution evidence: %s", e.kind)
					}
				}
			})
		}
	}
}

func TestRawOutputContinuationPreservesArgumentAndExecutesOnceWithoutAdvice(t *testing.T) {
	value := "α🚚\nconst marker = '<tool_call>';\n"
	full := "<tool_call><function=record><parameter=value>\n" + value + "\n</parameter></function></tool_call>"
	m := &rawContinuationModel{t: t, partial: full[:strings.Index(full, "🚚")], complete: full}
	path := filepath.Join(t.TempDir(), "effect")
	effects, judgments := 0, 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
		effects++
		if args["value"] != value {
			t.Errorf("argument changed: %q", args["value"])
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return tool.Result{}, err
		}
		defer f.Close()
		_, err = f.WriteString(args["value"].(string))
		return tool.Result{Text: "recorded"}, err
	}})
	r := &agent.Runner{Model: m, Registry: reg, Session: &nativeRecordingSink{}, SessionID: "raw-owner", MaxSteps: 2, MaxTokens: 262144, Judge: func(_ context.Context, _ decision.Request) (decision.Verdict, error) {
		judgments++
		return decision.Verdict{}, fmt.Errorf("unexpected review at physical limit")
	}}
	final, _, err := r.Run(t.Context(), "record the requested source once")
	if err != nil || final != "done" || r.Outcome.Status != outcome.Completed || effects != 1 || judgments != 0 || len(m.requests) != 3 {
		t.Fatalf("final=%q err=%v outcome=%+v effects=%d reviews=%d requests=%d", final, err, r.Outcome, effects, judgments, len(m.requests))
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != value {
		t.Fatalf("filesystem effect changed/repeated: %q %v", got, err)
	}
	if r.UsedIn != 337 || r.UsedOut != 20 || r.UsageUnknown {
		t.Fatalf("physical request accounting lost/doubled: %d/%d unknown=%v", r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
	users, results := 0, 0
	for _, msg := range m.requests[2] {
		if msg.Role == "user" {
			users++
		}
		if msg.Role == "tool" {
			results++
		}
		if strings.Contains(msg.Content, "Unverified unfinished") {
			t.Fatal("raw prefix duplicated as conversation prose")
		}
	}
	if users != 1 || results != 1 {
		t.Fatalf("extra recovery prompt or effect receipt: users=%d results=%d", users, results)
	}
}

func TestRawOutputCancellationNeverContinuesOrExecutes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &rawContinuationModel{t: t, partial: "<tool_call><function=record><parameter=value>unfinished", cancel: cancel}
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		t.Fatal("cancelled draft executed")
		return tool.Result{}, nil
	}})
	r := &agent.Runner{Model: m, Registry: reg, Session: &nativeRecordingSink{}, SessionID: "cancelled-raw"}
	_, _, _ = r.Run(ctx, "record once")
	if len(m.requests) != 1 || r.Outcome.Status == outcome.Completed {
		t.Fatalf("cancelled response continued/completed: requests=%d outcome=%+v", len(m.requests), r.Outcome)
	}
	retained := false
	for _, msg := range r.Continuation.Messages() {
		if strings.Contains(msg.Content, m.partial) {
			retained = true
		}
		if len(msg.ToolCalls) > 0 {
			t.Fatal("cancelled draft gained executable authority")
		}
	}
	if !retained {
		t.Fatal("cancelled raw evidence lost from retained history")
	}
}
