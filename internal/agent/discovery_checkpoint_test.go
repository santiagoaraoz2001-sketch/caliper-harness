package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/files"
)

const discoveryContinueOption = "continue productive work"
const discoveryActOption = "repair the latest rejected call"
const discoveryReconsiderOption = "reconsider the unsuccessful approach"

// Completed-response component double; no model inference. Tools use real files.
type discoveryModel struct {
	replies []llm.Reply
	seen    [][]llm.Message
}

func (m *discoveryModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	panic("native only")
}
func (m *discoveryModel) ChatTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, append([]llm.Message(nil), msgs...))
	if len(m.replies) == 0 {
		return llm.Reply{Message: llm.Message{Role: "assistant", Content: "done"}}, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}
func discoveryReply(names ...string) llm.Reply {
	r := llm.Reply{Message: llm.Message{Role: "assistant"}, FinishReason: "tool_calls"}
	for _, name := range names {
		r.Message.ToolCalls = append(r.Message.ToolCalls, llm.ToolCall{ID: name, Name: "read", Arguments: fmt.Sprintf(`{"path":%q,"limit":1}`, name)})
	}
	return r
}
func discoveryRunner(t *testing.T, m Model, judge JudgeFunc, sink SessionLog) *Runner {
	t.Helper()
	dir := t.TempDir()
	reg := tool.NewRegistry()
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte(fmt.Sprintf("actual source %d\nnext source line %d\n", i, i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w := &files.Workspace{Path: dir, Caps: config.Defaults().Tools.Bash}
	for _, def := range w.Definitions() {
		reg.Register(def)
	}
	return &Runner{Model: m, Registry: reg, Judge: judge, Threshold: .7, LoopStall: 5, LoopRepeat: 3, MaxSteps: 20, MaxRunSeconds: 1200, Session: sink, SessionID: "discovery", Workspace: dir}
}
func TestDiscoveryCheckpointCompletedWindowAndNextRequest(t *testing.T) {
	m := &discoveryModel{}
	for i := 0; i < 4; i++ {
		r := discoveryReply(fmt.Sprint(i))
		if i == 0 {
			r.Message.ReasoningContent = strings.Repeat("already located focused fix; ", 900)
		}
		m.replies = append(m.replies, r)
	}
	// Crossing cadence inside an array must wait for both real receipts.
	m.replies = append(m.replies, discoveryReply("4", "5"))
	calls := 0
	sink := &recordSink{}
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		if strings.Contains(q.Context, "already located focused fix") || !strings.Contains(q.Context, "OmittedDeliberation") || !strings.Contains(q.Context, "original bounded user task") || !strings.Contains(q.Context, "actual source 5") {
			t.Error("lost omission disclosure, intent or latest real source")
		}
		var packet struct {
			Checkpoint                       ProgressCheckpoint
			RemainingSteps, RemainingSeconds int
			RecentReceipts                   []loopObservation
			RecentCompletedReplies           []llm.Message
		}
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Checkpoint.Phase != "EXECUTE" || packet.Checkpoint.PATStep != "not-selected" {
			t.Error("ordinary coding mislabeled as planning/claim", packet.Checkpoint)
		}
		if packet.Checkpoint.RunStep != 6 || packet.RemainingSteps != 14 || packet.RemainingSeconds <= 0 || len(packet.RecentCompletedReplies) != 5 || len(packet.RecentReceipts) != 5 {
			t.Errorf("actual bounds/window %+v", packet)
		}
		if !packet.RecentReceipts[len(packet.RecentReceipts)-1].Truncated {
			t.Error("actual paged read fixture did not exercise continuation")
		}
		if strings.Contains(packet.Checkpoint.NextAction, "claimed task") {
			t.Error("ordinary advice fabricated claim")
		}
		for _, id := range []string{"4", "5"} {
			found := false
			for _, e := range rEntriesForSink(sink) {
				found = found || strings.Contains(e, `"ToolCallID":"`+id+`"`) || strings.Contains(e, `"tool_call_id":"`+id+`"`)
			}
			if !found {
				t.Error("judge ran before array settled", id)
			}
		}
		return decision.Verdict{Option: discoveryReconsiderOption, Confidence: .9}, nil
	}, sink)
	_, _, err := r.Run(context.Background(), "original bounded user task")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(m.seen) != 6 {
		t.Fatalf("checkpoint calls=%d requests=%d", calls, len(m.seen))
	}
	found := false
	for _, msg := range m.seen[5] {
		found = found || strings.Contains(msg.Content, r.Prompts.Text("continuation.discovery.reconsider")) && msg.Role == "user"
	}
	if !found {
		t.Fatal("action advice absent from NEXT actual model request")
	}
	if strings.Count(strings.Join(sink.kinds, ","), "guidance") != 1 {
		t.Fatal("advice not durably recorded")
	}
}
func rEntriesForSink(s *recordSink) []string { return s.texts }

func TestDiscoveryCheckpointTruthfulDispositions(t *testing.T) {
	for _, mode := range []string{"continue", "uncertain", "stop", "service error", "cancel", "judgment write", "guidance write", "absent sink"} {
		t.Run(mode, func(t *testing.T) {
			m := &discoveryModel{}
			for i := 0; i < 5; i++ {
				m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &discoveryFailureSink{recordSink: &recordSink{}}
			if mode == "judgment write" {
				s.fail = "judgment"
			}
			if mode == "guidance write" {
				s.fail = "guidance"
			}
			calls, lastContext := 0, ""
			r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				calls++
				lastContext = q.Context
				if mode == "service error" {
					return decision.Verdict{}, errors.New("actual D1 unavailable")
				}
				if mode == "cancel" {
					cancel()
				}
				v := decision.Verdict{Option: discoveryContinueOption, Confidence: .9}
				if mode == "stop" || mode == "uncertain" || mode == "guidance write" {
					v.Option = prompts.Builtin().Text("decision.discovery.reconsider")
				}
				if mode == "uncertain" {
					v.Confidence = .4
				}
				return v, nil
			}, s)
			if mode == "absent sink" {
				r.Session = nil
			}
			_, _, err := r.Run(ctx, "inspect relevant source then fix and check")
			if mode == "absent sink" {
				if calls != 0 || err != nil || len(m.seen) != 6 {
					t.Fatalf("missing receipt sink calls=%d err=%v context=%s", calls, err, lastContext)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("decision calls=%d", calls)
			}
			switch mode {
			case "continue", "uncertain", "stop", "service error":
				if err != nil || r.Outcome.Status != outcome.Completed || len(m.seen) != 6 {
					t.Fatalf("legitimate/advisory continuation %+v %v", r.Outcome, err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || r.Outcome.Status != outcome.Cancelled || len(m.seen) != 5 {
					t.Fatalf("cancel %+v %v", r.Outcome, err)
				}
			default:
				if err == nil || r.Outcome.Status != outcome.Failed || len(m.seen) != 5 {
					t.Fatalf("failure %+v %v", r.Outcome, err)
				}
			}
			if mode == "service error" {
				failures := 0
				for i, kind := range s.kinds {
					if kind == "judgment-error" && strings.Contains(s.texts[i], "actual D1 unavailable") {
						failures++
					}
					if kind == "guidance" || kind == decision.KindJudgment {
						t.Fatal("failed advice fabricated a nudge or verdict")
					}
				}
				if failures != 1 {
					t.Fatalf("actual unavailable advice not recorded: failures=%d", failures)
				}
			}
			if mode == "uncertain" || mode == "service error" {
				joined := ""
				for _, msg := range m.seen[5] {
					joined += msg.Content
				}
				if strings.Contains(joined, prompts.Builtin().Text("continuation.discovery.reconsider")) {
					t.Fatal("failed-confidence judgment injected a model nudge")
				}
			}
		})
	}
}

type discoveryFailureSink struct {
	*recordSink
	fail string
}

func (s *discoveryFailureSink) Append(ctx context.Context, id, kind, text string) error {
	if kind == s.fail {
		return fmt.Errorf("actual durable %s failure", kind)
	}
	return s.recordSink.Append(ctx, id, kind, text)
}

func TestDiscoveryCheckpointActualProgressRemainsVisibleToCadence(t *testing.T) {
	for _, kind := range []string{"write", "check"} {
		t.Run(kind, func(t *testing.T) {
			m := &discoveryModel{}
			for i := 0; i < 4; i++ {
				m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
			}
			args := `{"path":"new","content":"actual effect"}`
			if kind == "check" {
				args = `{}`
			}
			m.replies = append(m.replies, llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "progress", Name: kind, Arguments: args}}}, FinishReason: "tool_calls"})
			for i := 4; i < 9; i++ {
				m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
			}
			calls := 0
			r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				calls++
				if len(m.seen) != 5*calls {
					t.Error("actual progress disabled advisory cadence", kind, len(m.seen))
				}
				var packet progressPacket
				if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
					t.Fatal(err)
				}
				if calls == 1 && (packet.LatestSettled == nil || packet.LatestSettled.Tool != kind || packet.LatestSettled.Failed) {
					t.Fatal("actual effect/check absent from advisory evidence", packet.LatestSettled)
				}
				return decision.Verdict{Option: discoveryContinueOption, Confidence: .9}, nil
			}, &recordSink{})
			if kind == "write" {
				// The real workspace write definition was registered with read.
			} else {
				r.Prompts = prompts.Builtin()
				r.CodingRequired = true
				r.CheckCommand = "printf actual-check-completed"
				r.Registry.Register(r.CheckDefinition(bash.New(config.Defaults().Tools.Bash)))
			}
			_, _, err := r.Run(context.Background(), "perform bounded work")
			if err != nil || calls != 2 {
				t.Fatalf("actual progress cadence calls=%d error=%v", calls, err)
			}
			if kind == "check" && (r.Continuation.Coding == nil || !r.Continuation.Coding.Passed()) {
				t.Fatal("owning check did not pass")
			}
			if kind == "write" {
				b, e := os.ReadFile(filepath.Join(r.Workspace, "new"))
				if e != nil || string(b) != "actual effect" {
					t.Fatal("write fixture did not execute", e)
				}
			}
		})
	}
}

func TestDiscoveryCheckpointNativeTransportGuidance(t *testing.T) {
	n, judges := 0, 0
	var next []llm.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		n++
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		if e := json.NewDecoder(q.Body).Decode(&req); e != nil {
			t.Error(e)
		}
		var msg map[string]any
		finish := "tool_calls"
		if n <= 5 {
			msg = map[string]any{"reasoning_content": "completed bounded inspection", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint(n), "type": "function", "function": map[string]string{"name": "read", "arguments": fmt.Sprintf(`{"path":%q,"limit":1}`, fmt.Sprint(n-1))}}}}
		} else {
			next = req.Messages
			msg = map[string]any{"content": "done"}
			finish = "stop"
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": msg, "finish_reason": finish}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer srv.Close()
	r := discoveryRunner(t, llm.New(srv.URL, "component").WithToolProtocol("structured"), func(context.Context, decision.Request) (decision.Verdict, error) {
		judges++
		return decision.Verdict{Option: discoveryReconsiderOption, Confidence: .9}, nil
	}, &recordSink{})
	_, _, err := r.Run(context.Background(), "original exact user task")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range next {
		found = found || msg.Role == "user" && strings.Contains(msg.Content, r.Prompts.Text("continuation.discovery.reconsider"))
	}
	if n != 6 || judges != 1 || !found {
		t.Fatalf("actual transport requests=%d judges=%d advice=%t", n, judges, found)
	}
}

func TestDiscoveryCheckpointPreviousDeliveredAdvice(t *testing.T) {
	m := &discoveryModel{}
	for i := 0; i < 10; i++ {
		m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
	}
	calls := 0
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		var packet struct{ PreviousAdvice string }
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if calls == 1 && packet.PreviousAdvice != "" {
			t.Fatal("invented prior advice")
		}
		if calls == 2 && !strings.Contains(packet.PreviousAdvice, prompts.Builtin().Text("continuation.discovery.reconsider")) {
			t.Fatal("new successful reads erased delivered advice")
		}
		return decision.Verdict{Option: discoveryReconsiderOption, Confidence: .9}, nil
	}, &recordSink{})
	_, _, err := r.Run(context.Background(), "keep this same bounded task")
	if err != nil || calls != 2 {
		t.Fatalf("cadence/advice calls=%d err=%v", calls, err)
	}
}
