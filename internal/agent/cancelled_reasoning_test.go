package agent_test

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Exact received reasoning from the operator-cancelled native Laguna request,
// 2026-10-21 session c29b, archived event 36. Replay is not model inference.
//
//go:embed testdata/cancelled-laguna-reasoning.txt
var cancelledLagunaReasoning string

func TestCancelledCapturedLagunaReasoningRetainsResumeWithoutRetry(t *testing.T) {
	if len(cancelledLagunaReasoning) != 3271 || fmt.Sprintf("%x", sha256.Sum256([]byte(cancelledLagunaReasoning))) != "677270816d0a1625172ae1593c7d9a0f4d9f323b3129a96f811a0233dc99524c" {
		t.Fatal("captured reasoning drift")
	}
	received := make(chan struct{})
	var once sync.Once
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": cancelledLagunaReasoning}}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		w.(http.Flusher).Flush()
		<-req.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(llm.WithAuxObserver(t.Context(), func(_ context.Context, d llm.AuxDelta) error {
		if d.Field == "reasoning" && d.Delta == cancelledLagunaReasoning {
			once.Do(func() { close(received) })
		}
		return nil
	}))
	defer cancel()
	dir := t.TempDir()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: tool.NewRegistry(), Session: thinkingLog{dir: dir}, SessionID: "cancelled-capture", MaxTokens: 262144}
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(ctx, "original objective"); done <- err }()
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("captured reasoning was not received")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || r.Outcome.Status != outcome.Cancelled || requests != 1 {
			t.Fatalf("cancellation status/retry: %v %+v requests=%d", err, r.Outcome, requests)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not settle")
	}
	events, err := session.Read(dir, "cancelled-capture")
	if err != nil {
		t.Fatal(err)
	}
	tail := []session.Event{}
	for _, e := range events {
		if e.Kind != "continuation" {
			tail = append(tail, e)
		}
	}
	for _, saved := range [][]session.Event{events, tail} {
		c, _, err := session.Restore(saved, 262144)
		if err != nil {
			t.Fatal(err)
		}
		found, assistants := 0, 0
		for _, m := range c.Messages() {
			if m.Reasoning == cancelledLagunaReasoning {
				found++
			}
			if m.Role == "assistant" {
				assistants++
			}
			if len(m.ToolCalls) > 0 {
				t.Fatal("cancelled call gained execution authority")
			}
		}
		if found != 1 || assistants != 1 {
			t.Fatalf("restore with %d events: captured thought=%d assistant messages=%d; want one original reply", len(saved), found, assistants)
		}
	}
	notices := 0
	for _, e := range events {
		if e.Kind == "note" && strings.HasPrefix(e.Text, "partial reply") {
			notices++
			if strings.Contains(e.Text, "incomplete fields omitted") {
				t.Fatal("retention notice contradicts stored context")
			}
		}
		if e.Kind == "done" || e.Kind == "native_result" {
			t.Fatal("cancelled generation completed or executed", e.Kind)
		}
	}
	if notices != 1 {
		t.Fatalf("host diagnostic recorded %d times; want one note", notices)
	}
}

// Controlled transport-error return exercises auxiliary aliases and an unfinished
// native argument draft without inferring any executable arguments.
type partialErrorModel struct {
	reply llm.Reply
	calls int
}

func (m *partialErrorModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	panic("native path required")
}
func (m *partialErrorModel) ChatTools(context.Context, []llm.Message, []llm.ToolDefinition, func(string)) (llm.Reply, error) {
	m.calls++
	return m.reply, fmt.Errorf("transport interrupted with received fields")
}
func TestPartialFailureRetainsAliasesAndArgumentDraftWithoutRetry(t *testing.T) {
	want := llm.Reply{Message: llm.Message{Role: "assistant", Content: "received text", Reasoning: "reasoning α", ReasoningContent: "reasoning content β", Thinking: "thinking γ", ToolCalls: []llm.ToolCall{{ID: "draft", Name: "effect", Arguments: `{"unfinished":`}}}, Prefix: &llm.ResponsePrefix{PendingToolDraft: `{"another":`}}
	m := &partialErrorModel{reply: want}
	dir := t.TempDir()
	r := &agent.Runner{Model: m, Registry: tool.NewRegistry(), Session: thinkingLog{dir: dir}, SessionID: "partial-aliases"}
	_, _, err := r.Run(t.Context(), "retain partial evidence")
	if err == nil || r.Outcome.Status != outcome.Failed || m.calls != 1 {
		t.Fatalf("partial error retried or completed: %v %+v calls=%d", err, r.Outcome, m.calls)
	}
	events, err := session.Read(dir, "partial-aliases")
	if err != nil {
		t.Fatal(err)
	}
	readFailedNativeArchive(t, events, want, fmt.Errorf("transport interrupted with received fields"), 262144)
	c, _, err := session.Restore(events, 262144)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range c.Entries {
		if e.Message.Role == "assistant" {
			found = strings.Contains(e.Message.Content, "Unverified unfinished native argument draft (not executed):\n"+want.Prefix.PendingToolDraft)
		}
	}
	if !found || len(m.reply.Message.ToolCalls) != 1 || m.reply.Message.ToolCalls[0].Arguments != want.Message.ToolCalls[0].Arguments {
		t.Fatal("argument draft lost or original reply mutated")
	}
}
