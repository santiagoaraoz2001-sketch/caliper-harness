package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// lengthThenDoneSummaryModel is a ToolModel whose first summary response ends
// at the physical output limit with a resumable prefix (the c52 terminal
// shape) and whose continued response completes the same summary.
type lengthThenDoneSummaryModel struct {
	seen     [][]llm.Message
	partial  string
	complete string
}

func (m *lengthThenDoneSummaryModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("plain chat must not serve a ToolModel summary")
}

func (m *lengthThenDoneSummaryModel) ChatTools(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.seen = append(m.seen, append([]llm.Message(nil), msgs...))
	switch len(m.seen) {
	case 1:
		prompt := "rendered summary prompt"
		prefix := &llm.ResponsePrefix{
			Strategy:        llm.RulithRawPrefix,
			Prompt:          prompt,
			Raw:             m.partial,
			PromptSHA256:    fmt.Sprintf("%x", sha256.Sum256([]byte(prompt))),
			RequestSHA256:   strings.Repeat("b", 64),
			InitialState:    "normal",
			GeneratedTokens: 16384,
		}
		return llm.Reply{
			Message:            llm.Message{Role: "assistant", Content: m.partial},
			RawNative:          m.partial,
			NativeInitialState: "normal",
			Prefix:             prefix,
			OutputLimit:        true,
			FinishReason:       "length",
			Usage:              llm.Usage{In: 78574, Out: 16384, Reported: true},
		}, fmt.Errorf(`model server ended the reply with finish_reason "length" — output is partial`)
	case 2:
		return llm.Reply{
			Message:      llm.Message{Role: "assistant", Content: m.complete},
			RawNative:    m.complete,
			FinishReason: "stop",
			Usage:        llm.Usage{In: 94958, Out: 42, Reported: true},
		}, nil
	default:
		return llm.Reply{}, fmt.Errorf("unexpected summary call %d", len(m.seen))
	}
}

// A length terminal on the compaction summary call is a physical output
// boundary, not a failed summary: the same summary generation continues from
// its exact prefix and the compaction completes. Only a user stop may end it.
func TestCompactSummaryLengthTerminalContinuesSeamlessly(t *testing.T) {
	model := &lengthThenDoneSummaryModel{
		partial:  "Historical checkpoint. Observed: the converter repair was started and the literal check output was",
		complete: "Historical checkpoint. Observed: the converter repair was started and the literal check output was recorded; no verification result is claimed here.",
	}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 100000,
		TemplatesDir: testTemplateDir(t), Compaction: testCompaction(), Session: &recordSink{}, SessionID: "compact-continue"}
	c := session.Continuation{Version: 1, BasisTokens: 90000}
	c.Add("injected", "system", "sys", false)
	c.Add("intent", "user", "do the task", false)
	for i := 0; i < 8; i++ {
		c.Add("history", "user", strings.Repeat("old material ", 400), false)
	}
	c.BasisChars = contentSize(c.Messages())
	next, summary, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("compact ran=%t err=%v", ran, err)
	}
	if len(model.seen) != 2 {
		t.Fatalf("summary calls=%d, want 2 (limit segment + seamless continuation)", len(model.seen))
	}
	if len(model.seen[0]) != len(model.seen[1]) {
		t.Fatalf("continuation changed the summary request: %d msgs then %d", len(model.seen[0]), len(model.seen[1]))
	}
	if !reflect.DeepEqual(model.seen[0], model.seen[1]) {
		t.Fatal("continuation inserted or altered the summary request messages")
	}
	if summary != model.complete {
		t.Fatalf("summary=%q, want the completed generation %q", summary, model.complete)
	}
	if r.UsedIn != 78574+94958 || r.UsedOut != 16384+42 {
		t.Fatalf("usage accounting across segments lost: in=%d out=%d", r.UsedIn, r.UsedOut)
	}
	found := false
	for _, e := range next.Entries {
		if strings.Contains(e.Message.Content, "no verification result is claimed here") {
			found = true
		}
	}
	if !found {
		t.Fatal("completed summary missing from compacted context")
	}
}

// A user cancellation still ends the summary call promptly: continuation must
// never resurrect a cancelled generation.
func TestCompactSummaryCancellationDoesNotContinue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &cancelOnFirstSummaryModel{cancel: cancel}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 100000,
		TemplatesDir: testTemplateDir(t), Compaction: testCompaction(), Session: &recordSink{}, SessionID: "compact-cancel"}
	c := session.Continuation{Version: 1, BasisTokens: 90000}
	c.Add("injected", "system", "sys", false)
	c.Add("intent", "user", "do the task", false)
	for i := 0; i < 8; i++ {
		c.Add("history", "user", strings.Repeat("old material ", 400), false)
	}
	c.BasisChars = contentSize(c.Messages())
	_, _, ran, err := r.Compact(ctx, c)
	if ran || err == nil {
		t.Fatalf("cancelled compaction ran=%t err=%v", ran, err)
	}
	if model.calls != 1 {
		t.Fatalf("cancelled summary continued: calls=%d", model.calls)
	}
}

type cancelOnFirstSummaryModel struct {
	cancel context.CancelFunc
	calls  int
}

func (m *cancelOnFirstSummaryModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return "", "", llm.Usage{}, fmt.Errorf("plain chat must not serve a ToolModel summary")
}

func (m *cancelOnFirstSummaryModel) ChatTools(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	m.calls++
	m.cancel()
	prompt := "rendered summary prompt"
	prefix := &llm.ResponsePrefix{
		Strategy:        llm.RulithRawPrefix,
		Prompt:          prompt,
		Raw:             "partial",
		PromptSHA256:    fmt.Sprintf("%x", sha256.Sum256([]byte(prompt))),
		RequestSHA256:   strings.Repeat("b", 64),
		InitialState:    "normal",
		GeneratedTokens: 7,
	}
	return llm.Reply{
		Message:      llm.Message{Role: "assistant", Content: "partial"},
		RawNative:    "partial",
		Prefix:       prefix,
		OutputLimit:  true,
		FinishReason: "length",
		Usage:        llm.Usage{In: 10, Out: 7, Reported: true},
	}, fmt.Errorf("response cancelled: context canceled; reply is partial")
}
