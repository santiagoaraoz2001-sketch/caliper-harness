package tui

import (
	"os"
	"path/filepath"
	"specific-harness/internal/session"
	"testing"
)

func TestR2UserNoteSurvivesBeforeCaptureAndAcknowledgesOriginalArchive(t *testing.T) {
	m := New(testCfg(), "test")
	dir := t.TempDir()
	m.Chat.Running = true
	m.Chat.btw = &btwBox{owner: "original", archive: dir}
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "original task", false)
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Append(dir, "original", session.Event{Kind: "continuation", Text: raw}); err != nil {
		t.Fatal(err)
	}
	m.btwChat([]string{"Exact café\n  preserve spacing"})
	events, err := session.Read(dir, "original")
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore(events, 32768)
	if err != nil || len(restored.PendingNotes) != 1 {
		t.Fatalf("pre-capture note lost: %+v %v", restored, err)
	}
	n, ok := m.Chat.btw.popNote()
	if !ok || n != restored.PendingNotes[0] || n.SessionID != "original" {
		t.Fatal("queue changed envelope")
	}
	// UI or reviewer can finish this note during a later run; acknowledgement
	// stays in its original archive, so reopening that archive cannot replay it.
	if err := appendUserNote(dir, "btw_ack", n); err != nil {
		t.Fatal(err)
	}
	events, _ = session.Read(dir, "original")
	restored, _, err = session.Restore(events, 32768)
	if err != nil || len(restored.PendingNotes) != 0 {
		t.Fatalf("acknowledged note replayed: %+v %v", restored, err)
	}
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Chat.btw = &btwBox{owner: "refused", archive: blocked}
	m.btwChat([]string{"must not claim delivery"})
	if m.Chat.btw.pending() || lastAlert(m).Kind != "err" {
		t.Fatal("failed persistence queued or claimed success")
	}
}

func TestR2DismissedAlertHistoryDoesNotBecomeCurrentAgain(t *testing.T) {
	m := New(testCfg(), "test")
	m.alert("err", "old failed action")
	m.alert("info", "corrected action succeeded")
	m = key(m, "esc")
	if !m.alertHidden || len(m.Alerts) != 2 {
		t.Fatal("dismissal did not retain bounded history")
	}
	m.alert("warn", "new actual issue")
	if m.alertHidden || lastAlert(m).Text != "new actual issue" {
		t.Fatal("new issue stayed hidden")
	}
}
