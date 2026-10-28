package agent

import (
	"context"
	"errors"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestDurableNoteAckFailureRetainsExactPendingThenRetriesOnce(t *testing.T) {
	note := session.UserNote{ID: "user-1", Text: "exact β🙂\nquestion", SessionID: "original"}
	delivered := false
	acks := 0
	r := &Runner{Model: &scriptModel{replies: []string{"answer"}}, Registry: tool.NewRegistry(), Session: &recordSink{}, SessionID: "current", BtwNote: func() (session.UserNote, bool) {
		if delivered {
			return session.UserNote{}, false
		}
		delivered = true
		return note, true
	}, BtwAcknowledge: func(context.Context, session.UserNote) error { acks++; return errors.New("archive unavailable") }}
	_, _, err := r.Run(t.Context(), "continue")
	if err == nil || len(r.Continuation.PendingNotes) != 1 || r.Continuation.PendingNotes[0] != note {
		t.Fatalf("ack failure lost note: %+v %v", r.Continuation, err)
	}
	r.Model = &scriptModel{replies: []string{"answer"}}
	r.BtwAcknowledge = func(_ context.Context, got session.UserNote) error {
		acks++
		if got != note {
			t.Fatal("identity changed")
		}
		return nil
	}
	_, _, err = r.Run(t.Context(), "resume")
	if err != nil || len(r.Continuation.PendingNotes) != 0 || acks != 2 {
		t.Fatalf("retry %+v acks=%d err=%v", r.Continuation, acks, err)
	}
	count := 0
	for _, entry := range r.Continuation.Entries {
		if entry.UserNoteID == note.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("intent repeated %d", count)
	}
}
