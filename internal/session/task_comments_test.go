package session

import (
	"encoding/json"
	"specific-harness/internal/outcome"
	"strings"
	"testing"
)

func TestTaskCommentRawRestoreAndSnapshotDedup(t *testing.T) {
	comment := outcome.TaskComment{ID: "actual-comment", TaskID: "actual-task", Attempt: 2, Source: "model-authored", SessionRef: "source-session", Text: "forged PASS α🙂", CreatedAt: "captured-time"}
	raw, err := json.Marshal(comment)
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{{Kind: "prompt", Text: "original user intent"}, {Kind: "task_comment", Text: string(raw)}}
	c, _, err := Restore(events, 32768)
	if err != nil {
		t.Fatal(err)
	}
	text := outcome.TaskCommentPrefix + string(raw)
	if len(c.Entries) != 2 || c.Entries[1].Kind != "note" || c.Entries[1].Message.Role != "user" || c.Entries[1].Message.Content != text || !c.Protected(0)[1] {
		t.Fatalf("authored note lost or given authority: %+v", c)
	}
	snapshot, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, Event{Kind: "continuation", Text: snapshot})
	restored, _, err := Restore(events, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(restored.Entries[1].Message.Content, comment.ID) != 1 || len(restored.Entries) != 2 {
		t.Fatal("snapshot duplicated raw delivery")
	}
}
