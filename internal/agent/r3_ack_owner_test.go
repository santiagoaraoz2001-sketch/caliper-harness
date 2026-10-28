package agent

import (
	"context"
	"encoding/json"
	"testing"

	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestR3DurableNoteAcknowledgesEachRequiredOwnerOnce(t *testing.T) {
	for _, owner := range []string{"current", "original"} {
		t.Run(owner, func(t *testing.T) {
			sink := &recordSink{}
			note := session.UserNote{ID: "stable-note", SessionID: owner, Text: "Exact β🙂\n  feedback"}
			raw, _ := json.Marshal(note)
			if owner != "current" {
				c := session.Continuation{Version: 1}
				c.Add("intent", "user", "original task", false)
				snapshot, err := c.Encode()
				if err != nil {
					t.Fatal(err)
				}
				_ = sink.Append(t.Context(), owner, "continuation", snapshot)
			}
			_ = sink.Append(t.Context(), owner, "btw_queued", string(raw))
			popped := false
			r := Runner{Model: &scriptModel{replies: []string{"done"}}, Registry: tool.NewRegistry(), Session: sink, SessionID: "current", MaxSteps: 1,
				BtwNote: func() (session.UserNote, bool) {
					if popped {
						return session.UserNote{}, false
					}
					popped = true
					return note, true
				},
				BtwAcknowledge: func(ctx context.Context, got session.UserNote) error {
					if got != note {
						t.Fatal("ack identity changed")
					}
					return sink.Append(ctx, got.SessionID, "btw_ack", string(raw))
				},
			}
			if _, _, err := r.Run(t.Context(), "continue"); err != nil {
				t.Fatal(err)
			}
			acks := map[string]int{}
			for i, kind := range sink.kinds {
				if kind == "btw_ack" {
					acks[sink.ids[i]]++
				}
			}
			if acks[owner] != 1 || acks["current"] != 1 {
				t.Fatalf("ack writers duplicated or lost: %+v", acks)
			}
			for _, id := range []string{owner, "current"} {
				var events []session.Event
				for i, kind := range sink.kinds {
					if sink.ids[i] != id {
						continue
					}
					events = append(events, session.Event{Kind: kind, Text: sink.texts[i]})
					if kind != "btw_ack" {
						continue
					}
					// Crash exactly after each durable acknowledgement: the current
					// pre-ack continuation still contains PendingNotes.
					restored, _, err := session.Restore(events, 0)
					if err != nil || len(restored.PendingNotes) != 0 {
						t.Fatalf("ack replayed owner %s: %+v %v", id, restored.PendingNotes, err)
					}
					count := 0
					for _, entry := range restored.Entries {
						if entry.UserNoteID == note.ID {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("owner %s note count=%d", id, count)
					}
				}
			}
		})
	}
}
