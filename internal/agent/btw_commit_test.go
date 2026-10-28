package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

func TestBtwCommitAcknowledgesPersistedContinuation(t *testing.T) {
	sink := &recordSink{}
	note := "preserve Unicode α🙂 and\nparagraphs"
	r := Runner{Model: &scriptModel{replies: []string{"done"}}, Registry: tool.NewRegistry(), Session: sink, SessionID: "btw-commit", MaxSteps: 1,
		Btw: func() string { n := note; note = ""; return n }}
	if _, _, err := r.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	captured, persisted, committed := -1, -1, -1
	for i, kind := range sink.kinds {
		switch kind {
		case "btw_received":
			captured = i
		case "continuation":
			if captured >= 0 && committed < 0 {
				persisted = i
			}
		case "btw_committed":
			committed = i
		case "btw":
			t.Fatal("ambiguous legacy capture acknowledgement emitted")
		}
	}
	if !(captured >= 0 && captured < persisted && persisted < committed) {
		t.Fatalf("capture/persist/commit order %d/%d/%d", captured, persisted, committed)
	}
}

type failBtwCapture struct{ recordSink }

func (s *failBtwCapture) Append(ctx context.Context, id, kind, text string) error {
	if kind == "btw_received" {
		return errors.New("capture unavailable")
	}
	return s.recordSink.Append(ctx, id, kind, text)
}

func TestBtwCaptureFailurePreservesPoppedIntentWithoutAcknowledgement(t *testing.T) {
	sink := &failBtwCapture{}
	text := "preserve α🙂\n  exact indentation"
	note := text
	r := Runner{Model: &scriptModel{replies: []string{"must not run"}}, Registry: tool.NewRegistry(), Session: sink, SessionID: "capture-failure", MaxSteps: 1,
		Btw: func() string { n := note; note = ""; return n }}
	if _, _, err := r.Run(context.Background(), "continue"); err == nil {
		t.Fatal("capture failure hidden")
	}
	found := false
	for _, e := range r.Continuation.Entries {
		found = found || e.Message.Content == "Undelivered user comment: "+text
	}
	if !found {
		t.Fatal("popped user intent lost")
	}
	for _, kind := range sink.kinds {
		if strings.Contains(kind, "btw_committed") {
			t.Fatal("failed capture acknowledged")
		}
	}
}
