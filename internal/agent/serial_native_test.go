package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exact original worker733 NDJSON; this is a response replay, not new inference.
func TestSerialCaptured733ActualBashAndCheck(t *testing.T) {
	raw, err := os.ReadFile("testdata/serial-733.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "check_marker.py"), []byte("from pathlib import Path\nassert Path('marker.txt').read_bytes() == 'R20_MANUAL_ρ_full_suffix\\n'.encode()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	n := 0
	var next []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/show" {
			fmt.Fprint(w, `{"capabilities":["completion","tools","thinking"]}`)
			return
		}
		if req.URL.Path != "/api/chat" {
			t.Errorf("unexpected fixture path %s", req.URL.Path)
			http.Error(w, "wrong path", 400)
			return
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		n++
		if n == 1 {
			w.Write(raw)
			return
		}
		next = body.Messages
		fmt.Fprintln(w, `{"model":"qwen3:8b","message":{"role":"assistant","content":"model final"},"done":true,"done_reason":"stop","prompt_eval_count":200,"eval_count":5}`)
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	cfg.Model.Runtime = "ollama"
	reg := tool.NewRegistry()
	b := bash.NewAt(cfg.Tools.Bash, dir)
	reg.Register(b)
	sink := &nativeRecordingSink{}
	r := &agent.Runner{Model: llm.New(cfg.Model.Server, "qwen3:8b").WithRuntime(cfg.Model.Runtime), Registry: reg, MaxSteps: 3, Session: sink, CodingRequired: true, Workspace: dir, CheckCommand: "python3 check_marker.py"}
	r.Continuation.Coding = &session.CodingState{TaskID: "serial-marker", Workspace: dir, Command: r.CheckCommand, AcceptanceCommand: "test -f marker.txt"}
	reg.Register(r.CheckDefinition(b))
	var approvals []string
	g := auth.Gate{Mode: config.AuthAll, AskFunc: func(_ context.Context, p string) bool { approvals = append(approvals, p); return true }}
	r.Exec = g.Wrap(r.CodingExec(agent.Direct))
	_, _, err = r.Run(context.Background(), "actual captured serial marker/check component")
	marker, readErr := os.ReadFile(filepath.Join(dir, "marker.txt"))
	if err != nil || readErr != nil || string(marker) != "R20_MANUAL_ρ_full_suffix\n" || r.Outcome.Status != outcome.Completed || !r.Continuation.Coding.Passed() {
		t.Fatalf("marker=%q read=%v run=%v outcome=%+v", marker, readErr, err, r.Outcome)
	}
	if len(approvals) != 2 || !strings.Contains(approvals[0], "run bash?") || !strings.Contains(approvals[1], "run check?") {
		t.Fatalf("separate approvals=%q", approvals)
	}
	if len(next) < 3 {
		t.Fatalf("next request missing group %+v", next)
	}
	a, b1, b2 := next[len(next)-3], next[len(next)-2], next[len(next)-1]
	calls, ok := a["tool_calls"].([]any)
	if !ok || len(calls) != 2 || b1["tool_call_id"] != calls[0].(map[string]any)["id"] || b2["tool_call_id"] != calls[1].(map[string]any)["id"] || !strings.Contains(fmt.Sprint(b2["content"]), "declared check passed") {
		t.Fatalf("next wire group %+v", next)
	}
	if err := r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}

type serialModel struct {
	replies []llm.Reply
	seen    [][]llm.Message
}

func (m *serialModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("wrong protocol")
}
func (m *serialModel) ChatTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, msgs)
	if len(m.replies) == 0 {
		return llm.Reply{Message: llm.Message{Role: "assistant", Content: "final"}, FinishReason: "stop"}, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}
func serialCall(id, name, cmd string) llm.ToolCall {
	b, _ := json.Marshal(map[string]any{"cmd": cmd, "timeout": 30})
	return llm.ToolCall{ID: id, Name: name, Arguments: string(b)}
}
func serialRunner(t *testing.T, calls []llm.ToolCall) (*agent.Runner, string, *nativeRecordingSink) {
	t.Helper()
	dir := t.TempDir()
	reg := tool.NewRegistry()
	reg.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
	sink := &nativeRecordingSink{}
	return &agent.Runner{Model: &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}}}, Registry: reg, MaxSteps: len(calls) + 1, Session: sink}, dir, sink
}
func receipts(t *testing.T, r *agent.Runner) []session.Entry {
	t.Helper()
	if err := r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
	var out []session.Entry
	for _, e := range r.Continuation.Entries {
		if e.Message.Role == "tool" {
			out = append(out, e)
		}
	}
	return out
}

func TestSerialPreflightBeforeAnyEffect(t *testing.T) {
	for _, mode := range []string{"unknown second", "invalid second", "mixed", "duplicate id", "budget"} {
		t.Run(mode, func(t *testing.T) {
			calls := []llm.ToolCall{serialCall("first", "bash", "printf first > first"), serialCall("second", "bash", "printf second > second")}
			switch mode {
			case "unknown second":
				calls[1].Name = "missing"
			case "invalid second":
				calls[1].Arguments = `{"timeout":30}`
			case "duplicate id":
				calls[1].ID = calls[0].ID
			}
			r, dir, _ := serialRunner(t, calls)
			if mode == "budget" {
				r.MaxSteps = 1
			}
			if mode == "mixed" {
				r.Model.(*serialModel).replies[0].Message.Content = "```tool\ntool: bash\ncmd: true\n```"
			}
			_, _, err := r.Run(context.Background(), "preflight no partial effect")
			if _, e := os.Stat(filepath.Join(dir, "first")); !os.IsNotExist(e) {
				t.Fatalf("first effect ran: %v", e)
			}
			if mode == "budget" {
				if r.Outcome.Status != outcome.Stopped {
					t.Fatal(r.Outcome)
				}
				rs := receipts(t, r)
				if len(rs) != 2 || !strings.Contains(rs[0].Message.Content, "budget") {
					t.Fatalf("budget receipts %+v", rs)
				}
			}
			if mode == "unknown second" || mode == "invalid second" || mode == "mixed" {
				if err != nil {
					t.Fatal(err)
				}
				if len(receipts(t, r)) != 2 {
					t.Fatal("lost refused associations")
				}
			}
			if mode == "duplicate id" {
				if err == nil {
					t.Fatal("invalid group promoted")
				}
			}
		})
	}
}

func TestSerialFailureDenialAndRecordingStopTail(t *testing.T) {
	for _, mode := range []string{"failed second", "denied second", "record reply", "record tool", "record result"} {
		t.Run(mode, func(t *testing.T) {
			calls := []llm.ToolCall{serialCall("one", "bash", "printf first > first"), serialCall("two", "bash", "printf second > second; exit 7"), serialCall("three", "bash", "printf tail > tail")}
			if strings.HasPrefix(mode, "record") {
				calls[1] = serialCall("two", "bash", "printf second > second")
			}
			r, dir, sink := serialRunner(t, calls)
			if mode == "denied second" {
				calls[1] = serialCall("two", "bash", "printf second > second")
				count := 0
				g := auth.Gate{Mode: config.AuthAll, AskFunc: func(context.Context, string) bool { count++; return count != 2 }}
				r.Exec = g.Wrap(agent.Direct)
			}
			if mode == "record reply" {
				sink.fail = "native_reply"
			}
			if mode == "record tool" {
				sink.fail = "tool"
			}
			if mode == "record result" {
				sink.fail = "native_result"
			}
			_, _, err := r.Run(context.Background(), "serial stop remaining calls")
			first, e := os.ReadFile(filepath.Join(dir, "first"))
			if mode == "record reply" || mode == "record tool" {
				if !os.IsNotExist(e) {
					t.Fatal("effect before durable original")
				}
			} else if e != nil || string(first) != "first" {
				t.Fatalf("first effect lost %q %v", first, e)
			}
			if mode == "denied second" || strings.HasPrefix(mode, "record") {
				if _, e := os.Stat(filepath.Join(dir, "second")); !os.IsNotExist(e) {
					t.Fatalf("second should not execute: %v", e)
				}
			}
			if mode == "failed second" {
				if b, e := os.ReadFile(filepath.Join(dir, "second")); e != nil || string(b) != "second" {
					t.Fatalf("failed actual effect %q %v", b, e)
				}
			}
			if _, e := os.Stat(filepath.Join(dir, "tail")); !os.IsNotExist(e) {
				t.Fatal("tail executed")
			}
			if strings.HasPrefix(mode, "record") {
				if err == nil || r.Outcome.Status != outcome.Failed {
					t.Fatalf("record failure hidden %v %+v", err, r.Outcome)
				}
			} else {
				rs := receipts(t, r)
				if len(rs) != 3 || !strings.Contains(rs[2].Message.Content, "not executed") {
					t.Fatalf("unexecuted receipt %+v", rs)
				}
			}
		})
	}
}

func TestSerialProgressAdviceWaitsForWholeArrayAndDoesNotReplay(t *testing.T) {
	r, dir, sink := serialRunner(t, []llm.ToolCall{serialCall("one", "bash", "printf x >> repeats"), serialCall("two", "bash", "printf x >> repeats"), serialCall("three", "bash", "printf x >> repeats")})
	r.LoopRepeat, r.Threshold = 2, .5
	judgments := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		settled := 0
		for _, entry := range r.Continuation.Entries {
			if entry.Message.Role == "tool" {
				settled++
			}
		}
		if err := r.Continuation.Snapshot().Validate(); err != nil {
			t.Fatal(err)
		}
		if settled != 3 {
			t.Fatal("progress judge ran before all actual results settled")
		}
		return decision.Verdict{Option: q.Options[2], Confidence: 1}, nil
	}
	_, _, err := r.Run(t.Context(), "repeat per actual native call")
	b, _ := os.ReadFile(filepath.Join(dir, "repeats"))
	if err != nil || r.Outcome.Status != outcome.Completed || string(b) != "xxx" || judgments != 1 {
		t.Fatalf("advisory array failed: %q %+v %v", b, r.Outcome, err)
	}
	var events []session.Event
	for _, e := range sink.events {
		events = append(events, session.Event{Kind: e.kind, Text: e.text})
	}
	c, _, err := session.Restore(events, 32768)
	if err != nil {
		t.Fatal(err)
	}
	r.Continuation, r.Model, r.MaxSteps, r.Judge = c, &serialModel{}, 1, nil
	_, _, err = r.Run(t.Context(), "explicit fresh turn")
	b, _ = os.ReadFile(filepath.Join(dir, "repeats"))
	if err != nil || string(b) != "xxx" {
		t.Fatalf("saved effects replayed %q %v", b, err)
	}
}

func TestSerialCancellationSettlesBashAndSkipsTail(t *testing.T) {
	r, dir, _ := serialRunner(t, []llm.ToolCall{serialCall("one", "bash", "printf first > first; sleep 30; printf late > late"), serialCall("two", "bash", "printf tail > tail")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(ctx, "cancel actual held Bash"); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "first")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned first effect never entered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || r.Outcome.Status != outcome.Cancelled {
			t.Fatalf("cancel %v %+v", err, r.Outcome)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned Bash cancellation did not settle")
	}
	for _, name := range []string{"late", "tail"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("cancelled effect %s: %v", name, err)
		}
	}
	rs := receipts(t, r)
	if len(rs) != 2 || !strings.Contains(rs[1].Message.Content, "not executed") {
		t.Fatalf("cancel tail %+v", rs)
	}
}

func TestSerialCheckThenMutationRequiresFreshCheck(t *testing.T) {
	r, dir, _ := serialRunner(t, []llm.ToolCall{serialCall("check", "check", "true"), serialCall("change", "bash", "printf changed > changed")})
	r.CodingRequired, r.Workspace, r.CheckCommand = true, dir, "true"
	b, _ := r.Registry.Lookup("bash")
	r.Continuation.Coding = &session.CodingState{TaskID: "serial-current", Workspace: dir, Command: r.CheckCommand, AcceptanceCommand: "test ! -f changed"}
	r.Registry.Register(r.CheckDefinition(b))
	r.Exec = r.CodingExec(agent.Direct)
	_, _, err := r.Run(context.Background(), "a later effect invalidates earlier check")
	if err != nil || r.Outcome.Status != outcome.Stopped || r.Continuation.Coding.Passed() || r.Continuation.Coding.Receipt.ExitCode == nil || *r.Continuation.Coding.Receipt.ExitCode != 0 {
		t.Fatalf("stale check %v %+v %+v", err, r.Outcome, r.Continuation.Coding)
	}
	bts, e := os.ReadFile(filepath.Join(dir, "changed"))
	if e != nil || string(bts) != "changed" {
		t.Fatalf("mutation %q %v", bts, e)
	}
}

// One malformed completed array is one repair attempt, not five executions.
func TestSerialPreflightLoopAttemptAccounting(t *testing.T) {
	for _, mode := range []string{"feedback", "repeated", "budget", "record failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := make([]llm.ToolCall, 5)
			for i := range calls {
				calls[i] = serialCall(fmt.Sprint("slot", i), "bash", "printf forbidden > marker")
			}
			calls[4].Arguments = `{}`
			r, dir, sink := serialRunner(t, calls)
			model := r.Model.(*serialModel)
			r.LoopStall, r.Threshold = 5, .7
			if mode == "repeated" {
				r.MaxSteps = 25
				for i := 1; i < 5; i++ {
					next := model.replies[0]
					next.Message.ToolCalls = append([]llm.ToolCall(nil), calls...)
					for j := range next.Message.ToolCalls {
						next.Message.ToolCalls[j].ID = fmt.Sprintf("reply%d-slot%d", i, j)
					}
					model.replies = append(model.replies, next)
				}
			}
			if mode == "budget" {
				r.MaxSteps = 5
			}
			if mode == "record failure" {
				sink.fail = "native_result"
			}
			judgments := 0
			r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
				judgments++
				if mode != "repeated" {
					t.Errorf("one refused reply tripped before feedback: %s", req.Context)
				}
				var facts struct {
					Trip, PreflightRefusal          string
					Assigned, CurrentClaim, Settled *bool
					Latest, LatestDiscovery         json.RawMessage
				}
				if err := json.Unmarshal([]byte(req.Context), &facts); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(facts.PreflightRefusal, "no calls ran") || !strings.Contains(facts.PreflightRefusal, "cmd") || facts.Assigned == nil || *facts.Assigned || facts.CurrentClaim == nil || *facts.CurrentClaim || facts.Settled == nil || *facts.Settled || string(facts.Latest) != "null" || string(facts.LatestDiscovery) != "null" {
					t.Errorf("refusal/phase/execution facts wrong: %s", req.Context)
				}
				settled := 0
				for _, e := range r.Continuation.Entries {
					if e.Message.Role == "tool" {
						settled++
					}
				}
				if settled != 25 || len(model.seen) != 5 || !strings.Contains(facts.Trip, "5") {
					t.Errorf("trip before complete fifth group: replies=%d receipts=%d %s", len(model.seen), settled, req.Context)
				}
				return decision.Verdict{Option: req.Options[2], Confidence: .95}, nil
			}
			_, _, err := r.Run(t.Context(), "component preflight accounting")
			if _, e := os.Stat(filepath.Join(dir, "marker")); !os.IsNotExist(e) {
				t.Fatal("refused array executed", e)
			}
			if mode == "record failure" {
				if err == nil || r.Outcome.Status != outcome.Failed || judgments != 0 || len(model.seen) != 1 {
					t.Fatalf("record failure hidden: %v %+v judgments%d requests%d", err, r.Outcome, judgments, len(model.seen))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			results := receipts(t, r)
			want := 5
			if mode == "repeated" {
				want = 25
			}
			if len(results) != want {
				t.Fatalf("lost receipts: %d != %d", len(results), want)
			}
			for i, e := range results {
				wantID := calls[i%5].ID
				if i >= 5 {
					wantID = fmt.Sprintf("reply%d-slot%d", i/5, i%5)
				}
				if e.Kind != "failure" || e.Message.ToolCallID != wantID || !strings.Contains(e.Message.Content, "no calls ran") {
					t.Fatalf("receipt changed %+v", e)
				}
			}
			switch mode {
			case "feedback":
				if judgments != 0 || len(model.seen) != 2 || r.Outcome.Status != outcome.Completed {
					t.Fatalf("feedback blocked: judgments%d requests%d %+v", judgments, len(model.seen), r.Outcome)
				}
				next := model.seen[1]
				if len(next) < 6 {
					t.Fatal("missing next group", next)
				}
				for i, msg := range next[len(next)-5:] {
					if msg.Role != "tool" || msg.ToolCallID != calls[i].ID || !strings.Contains(msg.Content, "cmd") {
						t.Fatalf("feedback missing %+v", msg)
					}
				}
			case "budget":
				if judgments != 0 || len(model.seen) != 1 || r.Outcome.Status != outcome.Stopped {
					t.Fatalf("five charges lost: judgments%d requests%d %+v", judgments, len(model.seen), r.Outcome)
				}
			case "repeated":
				if judgments != 1 || len(model.seen) != 5 || r.Outcome.Status != outcome.Stopped {
					t.Fatalf("repeated refusal escaped: judgments%d requests%d %+v", judgments, len(model.seen), r.Outcome)
				}
			}
		})
	}
}

// Removing the storage wall admits a complete large array without shell argument limits.
func TestSerialLargeGroupDurableAndCorrelated(t *testing.T) {
	value := strings.Repeat("x", 1100000) + "\nquoted:\"é"
	args, _ := json.Marshal(map[string]string{"value": value})
	calls := []llm.ToolCall{serialCall("first", "bash", "printf first > first"), {ID: "large", Name: "echo", Arguments: string(args)}}
	r, dir, _ := serialRunner(t, calls)
	archive := t.TempDir()
	r.Session, r.SessionID = completedLog{dir: archive}, "large"
	effects := 0
	r.Registry.Register(tool.Definition{Name: "echo", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
		events, err := session.Read(archive, "large")
		if err != nil {
			return tool.Result{}, err
		}
		found := false
		for _, e := range events {
			if e.Kind == "native_reply" {
				var original session.Entry
				if json.Unmarshal([]byte(e.Text), &original) == nil && original.Message.ToolCalls[1].Arguments == string(args) {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("effect reached before exact large native reply was durable")
		}
		if a["value"] != value {
			t.Fatal("large decoded argument changed")
		}
		effects++
		return tool.Result{Text: "observed"}, nil
	}})
	if _, _, err := r.Run(context.Background(), "source-sized valid native array"); err != nil || effects != 1 {
		t.Fatalf("large array refused %v effects%d", err, effects)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "first")); err != nil || string(b) != "first" {
		t.Fatal("first actual serial effect lost", err)
	}
	events, err := session.Read(archive, "large")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := session.Restore(events, 0)
	if err != nil || c.Validate() != nil {
		t.Fatal("large correlated array cannot reopen", err)
	}
}
