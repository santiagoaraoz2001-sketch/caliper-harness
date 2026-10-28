package session

import (
	"encoding/json"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"testing"
)

func TestQueuedUserNoteSurvivesSnapshotAndAckIsIdempotent(t *testing.T) {
	note := UserNote{ID: "note-1", Text: "exact β🙂\nline2", SessionID: "original"}
	raw, _ := json.Marshal(note)
	c := Continuation{Version: 1}
	c.Add("intent", "user", "task", false)
	snapshot, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{{Kind: "btw_queued", Text: string(raw)}, {Kind: "continuation", Text: snapshot}}
	restored, _, err := RestoreWithPrompts(events, 0, prompts.Builtin())
	if err != nil || len(restored.PendingNotes) != 1 || restored.PendingNotes[0] != note {
		t.Fatalf("queued note lost %+v %v", restored, err)
	}
	events = append(events, Event{Kind: "btw_ack", Text: string(raw)}, Event{Kind: "btw_ack", Text: string(raw)})
	restored, _, err = RestoreWithPrompts(events, 0, prompts.Builtin())
	if err != nil || len(restored.PendingNotes) != 0 {
		t.Fatalf("acked note pending %+v %v", restored, err)
	}
	seen := 0
	for _, entry := range restored.Entries {
		if entry.UserNoteID == note.ID {
			seen++
			if entry.Message.Content != "USER COMMENT: "+note.Text {
				t.Fatal("text changed")
			}
		}
	}
	if seen != 1 {
		t.Fatalf("ack restoration duplicated note %d", seen)
	}
}

func TestConsumedAskAnswerProtectsEntireNativeGroup(t *testing.T) {
	c := Continuation{Version: 1, Entries: []Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "choose identity"}}, {Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "q", Name: "ask", Arguments: `{"question":"Aesthetic?"}`}}}}, {Kind: "output", Message: llm.Message{Role: "tool", Name: "ask", ToolCallID: "q", Content: "industrial editorial, teal only for navigation"}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	keep := c.Protected(0)
	if !keep[1] || !keep[2] {
		t.Fatal("consumed human answer lost", keep)
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := RestoreWithPrompts([]Event{{Kind: "continuation", Text: raw}}, 0, prompts.Builtin())
	if err != nil || !restored.Protected(0)[2] {
		t.Fatal("restored answer lost", err)
	}
}
