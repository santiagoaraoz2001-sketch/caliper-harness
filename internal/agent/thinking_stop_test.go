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

// Exact API output from the captured Qwen parser-loss replay; SHA256 547fceb3071ac4b5bcf43b10d03ed0f8e7ccd9b3ca7238c7dc6de15a5a63798c.
//
//go:embed testdata/thinking-qwen-empty-stop.ndjson
var thinkingQwenEmpty []byte

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

func TestThinkingContinuesCaptured227WithoutAddedPrompt(t *testing.T) {
	assertThinkingContinues(t, thinking227, 5347, 678)
}
func TestThinkingContinuesCapturedQwenWithoutAddedPrompt(t *testing.T) {
	assertThinkingContinues(t, thinkingQwenEmpty, 12033, 4789)
}
func assertThinkingContinues(t *testing.T, raw []byte, input, output int) {
	t.Helper()
	dir := t.TempDir()
	r, latest, calls := thinkingRunner(t, raw, thinkingLog{dir: dir})
	_, _, err := r.Run(context.Background(), "inspect the project")
	if err != nil || *calls != 2 || r.Outcome.Status != outcome.Completed || r.UsedIn != input || r.UsedOut != output {
		t.Fatalf("%+v %v calls=%d", r.Outcome, err, *calls)
	}
	var thought strings.Builder
	dec := json.NewDecoder(bytes.NewReader(raw))
	for dec.More() {
		var row struct{ Message struct{ Thinking string } }
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		thought.WriteString(row.Message.Thinking)
	}
	found := false
	users := 0
	for _, m := range *latest {
		if m.Role == "user" {
			users++
			if m.Content != "inspect the project" {
				t.Fatalf("injected prompt %q", m.Content)
			}
		}
		found = found || m.Thinking == thought.String()
	}
	if !found || users != 1 {
		t.Fatalf("lost thinking or added prompt: %+v", *latest)
	}
	events, err := session.Read(dir, "thinking")
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore(events, 32768)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, e := range restored.Entries {
		found = found || e.Message.Thinking == thought.String()
	}
	if !found {
		t.Fatal("lost saved original thinking")
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
	r.MaxSteps = 1
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
		t.Fatal("thinking history must remain retained exactly once", retained)
	}
	r, _, _ = thinkingRunner(t, body, nil)
	r.MaxSteps = 1
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
	for _, kind := range []string{"reasoning", "native_reply", "note"} {
		t.Run(kind, func(t *testing.T) {
			r, _, calls := thinkingRunner(t, thinking227, thinkingLog{dir: t.TempDir(), fail: kind})
			_, _, err := r.Run(context.Background(), "retained failure")
			if !errors.Is(err, thinkingRecordError) || r.Outcome.Status != outcome.Failed || *calls != 1 {
				t.Fatalf("record failure hidden: %+v %v", r.Outcome, err)
			}
		})
	}
}
