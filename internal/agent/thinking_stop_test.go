package agent_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exact root native worker227 response, SHA a73de94f…; replay, not inference.
//
//go:embed testdata/thinking-227.ndjson
var thinking227 []byte

var thinkingRecordError = errors.New("controlled thinking record failure")

type thinkingLog struct{ dir, fail string }

func (s thinkingLog) Append(_ context.Context, id, kind, text string) error {
	if kind == s.fail || (s.fail == "fragment" && kind == "reasoning" && strings.HasPrefix(text, "offset=")) {
		return thinkingRecordError
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

func thinkingRunner(t *testing.T, raw []byte, sink agent.SessionLog) (*agent.Runner, *[]llm.Message, *int) {
	t.Helper()
	calls := 0
	var latest []llm.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			fmt.Fprint(w, `{"capabilities":["tools"]}`)
			return
		}
		if r.URL.Path != "/api/chat" {
			t.Errorf("unbound fixture path: %s", r.URL)
			http.Error(w, "wrong fixture path", 400)
			return
		}
		var request struct{ Messages []llm.Message }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		latest = request.Messages
		calls++
		if calls == 1 {
			_, _ = w.Write(raw)
		} else {
			fmt.Fprintln(w, `{"message":{"content":"resumed without checking"},"done":true,"done_reason":"stop"}`)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.Model.Server, cfg.Model.Runtime = srv.URL, "ollama"
	reg := tool.NewRegistry()
	reg.Register(bash.New(cfg.Tools.Bash))
	r := &agent.Runner{Model: llm.New(cfg.Model.Server, "fixture").WithRuntime(cfg.Model.Runtime), Registry: reg, Session: sink, SessionID: "thinking", MaxSteps: 3, Out: &bytes.Buffer{}}
	r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		t.Error("nonexecuted reasoning entered the tool executor")
		return tool.Result{}, errors.New("unexpected execution")
	}
	return r, &latest, &calls
}

func TestThinkingStopCaptured227AndExplicitResume(t *testing.T) {
	dir := t.TempDir()
	r, latest, calls := thinkingRunner(t, thinking227, thinkingLog{dir: dir})
	visible := ""
	r.OnToken = func(s string) { visible += s }
	_, _, err := r.Run(context.Background(), "inspect the project")
	if err != nil || r.Outcome.Status != outcome.Stopped || r.Outcome.ExitCode() != 1 || *calls != 1 || visible != "" || r.UsedIn != 5347 || r.UsedOut != 678 || r.UsageUnknown {
		t.Fatalf("captured completed thinking disposition: %+v %v calls=%d usage=%d/%d", r.Outcome, err, *calls, r.UsedIn, r.UsedOut)
	}
	var thought strings.Builder
	dec := json.NewDecoder(bytes.NewReader(thinking227))
	for dec.More() {
		var row struct{ Message struct{ Thinking string } }
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		thought.WriteString(row.Message.Thinking)
	}
	events, err := session.Read(dir, "thinking")
	if err != nil {
		t.Fatal(err)
	}
	continuation, _, err := session.Restore(events, 32768)
	if err != nil {
		t.Fatal(err)
	}
	found, header := false, false
	for _, e := range events {
		if e.Kind == "reasoning" && strings.Contains(e.Text, `"Served":"qwen3:8b"`) && strings.Contains(e.Text, `"In":5347`) {
			header = true
		}
	}
	for _, e := range continuation.Entries {
		if e.Message.Thinking == thought.String() {
			found = e.Kind == "reply" && e.Message.Content == "" && len(e.Message.ToolCalls) == 0
		}
	}
	if !found || !header {
		t.Fatal("original thinking/identity/per-turn usage not preserved")
	}
	r.Continuation = continuation
	r.CodingRequired, r.Workspace, r.CheckCommand = true, t.TempDir(), "true"
	r.MaxSteps = 1 // Explicit resume consumes its step; D18 has no remaining host-check admission.
	_, _, err = r.Run(context.Background(), "resume explicitly")
	if err != nil || *calls != 2 || r.Outcome.Status != outcome.Stopped || r.Continuation.Coding.Passed() || r.Continuation.Coding.Closed {
		t.Fatalf("resume inferred check or did not run: %+v %v calls=%d", r.Outcome, err, *calls)
	}
	found = false
	for _, msg := range *latest {
		found = found || msg.Thinking == thought.String()
	}
	if !found {
		t.Fatal("explicit next native request lost original thinking")
	}
}

func oversizedThinking() ([]byte, string) {
	thought := strings.Repeat("é \t\n", 260000)
	var body bytes.Buffer
	for i := 0; i < len(thought); i += 10000 {
		end := min(i+10000, len(thought)) // Pattern is five bytes; cut is UTF8-safe.
		_ = json.NewEncoder(&body).Encode(map[string]any{"message": map[string]string{"thinking": thought[i:end]}})
	}
	body.WriteString("{\"model\":\"oversize\",\"message\":{},\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":19,\"eval_count\":7}\n")
	return body.Bytes(), thought
}

func TestThinkingStopOversizeReadableRawAndBoundedResume(t *testing.T) {
	body, thought := oversizedThinking()
	dir := t.TempDir()
	r, _, calls := thinkingRunner(t, body, thinkingLog{dir: dir})
	_, _, err := r.Run(context.Background(), "keep original provenance")
	if err != nil || r.Outcome.Status != outcome.Stopped || *calls != 1 {
		t.Fatalf("oversize stopped disposition: %+v %v", r.Outcome, err)
	}
	events, err := session.Read(dir, "thinking")
	if err != nil {
		t.Fatal("raw archive unreadable:", err)
	}
	var entry session.Entry
	found := false
	for _, e := range events {
		if e.Kind == "native_reply" {
			if err := json.Unmarshal([]byte(e.Text), &entry); err != nil {
				t.Fatal(err)
			}
			found = entry.Message.Thinking == thought && entry.Message.Content == "" && len(entry.Message.ToolCalls) == 0
		}
	}
	if !found {
		t.Fatal("large original thinking fields not retained exactly")
	}
	continuation, notice, err := session.Restore(events, 32768)
	if err != nil || continuation.Validate() != nil || !strings.Contains(notice, "retained history") {
		t.Fatalf("smaller-window pressure disclosure missing: %s %v", notice, err)
	}
	retained := 0
	for _, e := range continuation.Entries {
		if e.Message.Thinking == thought {
			retained++
		}
	}
	if retained != 1 {
		t.Fatal("thinking-only stop must remain retained without covert resume", retained)
	}
	r, _, _ = thinkingRunner(t, body, nil)
	_, _, err = r.Run(context.Background(), "sessionless library")
	if err != nil || r.Outcome.Status != outcome.Stopped {
		t.Fatalf("sessionless stopped disposition: %+v %v", r.Outcome, err)
	}
	r, _, _ = thinkingRunner(t, body, thinkingLog{dir: t.TempDir(), fail: "native_reply"})
	_, _, err = r.Run(context.Background(), "incomplete archive")
	if !errors.Is(err, thinkingRecordError) || r.Outcome.Status != outcome.Failed {
		t.Fatalf("native archive failure hidden: %+v %v", r.Outcome, err)
	}
}

func TestThinkingStopRecordingFailures(t *testing.T) {
	for _, kind := range []string{"reasoning", "native_reply", "stopped", "continuation"} {
		t.Run(kind, func(t *testing.T) {
			r, _, calls := thinkingRunner(t, thinking227, thinkingLog{dir: t.TempDir(), fail: kind})
			_, _, err := r.Run(context.Background(), "retained failure")
			if !errors.Is(err, thinkingRecordError) || r.Outcome.Status != outcome.Failed || *calls != 1 {
				t.Fatalf("record failure hidden: %+v %v", r.Outcome, err)
			}
		})
	}
}
