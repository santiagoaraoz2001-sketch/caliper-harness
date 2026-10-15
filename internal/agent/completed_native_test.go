package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Controlled completed-response double; no inference or tool effects.
type completedModel struct {
	reply    llm.Reply
	inputs   [][]llm.Message
	cancel   context.CancelFunc
	protocol string
}

func (m *completedModel) ToolProtocol() string {
	if m.protocol != "" {
		return m.protocol
	}
	return "structured"
}
func (m *completedModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	panic("native path required")
}
func (m *completedModel) ChatTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.inputs = append(m.inputs, append([]llm.Message(nil), msgs...))
	if m.cancel != nil {
		m.cancel()
	}
	return m.reply, nil
}

var completedWriteError = errors.New("controlled completed archive write failure")

type completedLog struct{ dir, fail string }

func (s completedLog) Append(_ context.Context, id, kind, text string) error {
	if kind == s.fail || s.fail == "fragment" && kind == "reasoning" && strings.HasPrefix(text, "offset=") {
		return completedWriteError
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}
func completedRunner(t *testing.T, m *completedModel, sink agent.SessionLog) *agent.Runner {
	return &agent.Runner{Model: m, Registry: tool.NewRegistry(), Session: sink, SessionID: "completed", MaxSteps: 1, MaxTokens: 65536, Out: &bytes.Buffer{}, CodingRequired: true, Workspace: t.TempDir(), CheckCommand: "true"}
}
func completedArchive(t *testing.T, events []session.Event, want llm.Reply, rawOnly bool) {
	t.Helper()
	var raw strings.Builder
	replies, header := 0, false
	for _, e := range events {
		if e.Kind == "reply" {
			t.Error("duplicate text reply")
		}
		if e.Kind == "native_reply" {
			replies++
		}
		if e.Kind != "reasoning" {
			continue
		}
		if !strings.HasPrefix(e.Text, "offset=") {
			var h struct {
				Usage   llm.Usage
				Bytes   int
				RawOnly bool
			}
			if err := json.Unmarshal([]byte(e.Text), &h); err != nil {
				t.Fatal(err)
			}
			header = h.Usage == want.Usage && h.Bytes > 0 && h.RawOnly == rawOnly
			continue
		}
		p, frag, ok := strings.Cut(e.Text, "\n")
		var offset, total int
		if _, err := fmt.Sscanf(p, "offset=%d total=%d", &offset, &total); err != nil || !ok || offset != raw.Len() || len(frag) > 32<<10 || !utf8.ValidString(frag) || offset+len(frag) > total {
			t.Fatal("invalid raw fragment", p)
		}
		raw.WriteString(frag)
	}
	var got llm.Reply
	if json.Unmarshal([]byte(raw.String()), &got) != nil || !reflect.DeepEqual(got, want) || replies != 1 || !header {
		t.Fatalf("original reply not retained: header=%t nativeReplies=%d bytes=%d", header, replies, raw.Len())
	}
}
func TestCompletedNativeAllFieldsAndExplicitNextTurn(t *testing.T) {
	for _, reported := range []bool{true, false} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			want := llm.Reply{Message: llm.Message{Role: "assistant", Content: "model-reported answer", Thinking: strings.Repeat("é", 17000), Reasoning: "```tool\ntool: bash\ncmd: printf forbidden\n```", ReasoningContent: "original Δ\r\n"}, Served: "provider checkpoint α", FinishReason: "stop", Usage: llm.Usage{In: 73, Out: 19, Reported: reported}}
			if !reported {
				want.Usage = llm.Usage{}
			}
			m := &completedModel{reply: want}
			dir := t.TempDir()
			r := completedRunner(t, m, completedLog{dir: dir})
			effects := 0
			r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				effects++
				return tool.Result{}, nil
			}
			final, _, err := r.Run(context.Background(), "original intent")
			if err != nil || final != want.Message.Content || r.Outcome.Status != outcome.Stopped || effects != 0 || len(m.inputs) != 1 || r.UsageUnknown == reported {
				t.Fatalf("existing outcome/usage/effects: %+v %v effects=%d", r.Outcome, err, effects)
			}
			events, err := session.Read(dir, "completed")
			if err != nil {
				t.Fatal(err)
			}
			completedArchive(t, events, want, false)
			c, _, err := session.Restore(events, 65536)
			if err != nil {
				t.Fatal(err)
			}
			for _, entries := range [][]session.Entry{r.Continuation.Entries, c.Entries} {
				count := 0
				for _, e := range entries {
					if reflect.DeepEqual(e.Message, want.Message) {
						count++
					}
				}
				if count != 1 {
					t.Fatal("original message duplicated/lost", count)
				}
			}
			r.Continuation = c
			if _, _, err = r.Run(context.Background(), "explicit next turn"); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, msg := range m.inputs[1] {
				if reflect.DeepEqual(msg, want.Message) {
					count++
				}
			}
			if count != 1 {
				t.Fatal("next input lost/duplicated original fields", count)
			}
		})
	}
}
func TestCompletedNativeLargeExactArchiveAndSmallerResume(t *testing.T) {
	want := llm.Reply{Message: llm.Message{Role: "assistant", Content: "short final", Thinking: strings.Repeat("é", 600000)}, Served: "large original", FinishReason: "stop"}
	dir := t.TempDir()
	r := completedRunner(t, &completedModel{reply: want}, completedLog{dir: dir})
	if _, _, err := r.Run(context.Background(), "original intent"); err != nil || r.Outcome.Status != outcome.Stopped {
		t.Fatal(r.Outcome, err)
	}
	events, err := session.Read(dir, "completed")
	if err != nil {
		t.Fatal(err)
	}
	completedArchive(t, events, want, false)
	c, notice, err := session.Restore(events, 65536)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := c.Encode()
	if err != nil || len(encoded) < len(want.Message.Thinking) || !strings.Contains(notice, "retained history") {
		t.Fatal("smaller-window pressure or retained thought lost", len(encoded), err, notice)
	}
	retained := 0
	for _, e := range c.Entries {
		if e.Message.Thinking == want.Message.Thinking {
			retained++
		}
	}
	if retained != 1 {
		t.Fatal("completed thought must survive resume exactly once", retained)
	}
	r = completedRunner(t, &completedModel{reply: want}, nil)
	final, _, err := r.Run(context.Background(), "no sink")
	if err != nil || r.Outcome.Status != outcome.Stopped || final != want.Message.Content {
		t.Fatal("sessionless completed response disposition", r.Outcome, err)
	}
	found := false
	for _, e := range r.Continuation.Entries {
		found = found || reflect.DeepEqual(e.Message, want.Message)
	}
	if !found {
		t.Fatal("sessionless in-memory original lost")
	}
}
func TestCompletedNativeRecordingFailureAndCancel(t *testing.T) {
	for _, fail := range []string{"reasoning", "fragment", "native_reply"} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			r := completedRunner(t, &completedModel{reply: llm.Reply{Message: llm.Message{Role: "assistant", Content: "visible final", Reasoning: "original"}, FinishReason: "stop"}}, completedLog{dir: dir, fail: fail})
			final, _, err := r.Run(context.Background(), "record failure")
			if !errors.Is(err, completedWriteError) || r.Outcome.Status != outcome.Failed || final != "visible final" {
				t.Fatal("recording error swallowed", r.Outcome, err)
			}
			events, e := session.Read(dir, "completed")
			if e != nil {
				t.Fatal(e)
			}
			for _, event := range events {
				if event.Kind == "done" || event.Kind == "tool" || event.Kind == "native_result" {
					t.Fatal("false completion/effect", event.Kind)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := llm.Reply{Message: llm.Message{Role: "assistant", Content: "returned before settlement", ReasoningContent: "original"}, FinishReason: "stop"}
	dir := t.TempDir()
	r := completedRunner(t, &completedModel{reply: want, cancel: cancel}, completedLog{dir: dir})
	final, _, err := r.Run(ctx, "cancellation boundary")
	if !errors.Is(err, context.Canceled) || r.Outcome.Status != outcome.Cancelled || final != want.Message.Content {
		t.Fatal(r.Outcome, err)
	}
	events, e := session.Read(dir, "completed")
	if e != nil {
		t.Fatal(e)
	}
	completedArchive(t, events, want, false)
	for _, event := range events {
		if event.Kind == "done" {
			t.Fatal("cancelled response completed")
		}
	}
}

func TestCompletedNativeLargeContentDurableBeforeTextEffects(t *testing.T) {
	content := strings.Repeat("x", 1100000) + "\n```tool\ntool: bash\ncmd: printf forbidden\n```"
	call, found, repair := tool.Extract(content)
	if !found || call.Name != "bash" || repair != "" {
		t.Fatal("instrument did not supply an otherwise valid text call", call, found, repair)
	}
	want := llm.Reply{Message: llm.Message{Role: "assistant", Content: content}, FinishReason: "stop"}
	dir := t.TempDir()
	r := completedRunner(t, &completedModel{reply: want, protocol: "text"}, completedLog{dir: dir})
	r.Registry.Register(bash.New(config.Defaults().Tools.Bash))
	effects := 0
	r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{Text: "unexpected"}, nil
	}
	final, _, err := r.Run(context.Background(), "oversized complete content")
	if err != nil || r.Outcome.Status != outcome.Stopped || effects != 1 || !strings.Contains(final, "stopped after 1 steps") {
		t.Fatal("complete source-sized reply was refused or falsely completed", r.Outcome, err, effects)
	}
	events, e := session.Read(dir, "completed")
	if e != nil {
		t.Fatal(e)
	}
	var raw strings.Builder
	for _, event := range events {
		if event.Kind == "reasoning" && strings.HasPrefix(event.Text, "offset=") {
			_, fragment, _ := strings.Cut(event.Text, "\n")
			raw.WriteString(fragment)
		}
		if event.Kind == "done" {
			t.Fatal("unverified tool call falsely completed task")
		}
	}
	var got llm.Reply
	if json.Unmarshal([]byte(raw.String()), &got) != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("large original not archived")
	}
	if _, _, e = session.Restore(events, 65536); e != nil {
		t.Fatal("factual failure restore invalid", e)
	}
}
