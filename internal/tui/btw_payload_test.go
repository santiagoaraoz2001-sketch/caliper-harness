package tui

import "testing"

func TestBtwPreservesUserSpacing(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.btw = &btwBox{owner: "fixture", archive: t.TempDir()}
	want := "Keep this exact shape: α  →  β, with  two  spaces."
	m = submit(m, "/btw "+want)
	if got := m.Chat.btw.pop(); got != want {
		t.Fatalf("user feedback rewritten: got=%q want=%q", got, want)
	}
}
