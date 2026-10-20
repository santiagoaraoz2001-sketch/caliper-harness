package tui

import (
	"strings"
	"testing"
)

func TestStreamActivityVisibleWithoutChangingConversation(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width, m.Height = 80, 24
	m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 9
	before := len(m.Chat.Lines)
	for _, pair := range [][2]string{{"thinking", "Thinking…"}, {"tool", "Receiving tool call…"}, {"active", "Server is responding…"}, {"", "Waiting for model…"}} {
		m.onEvent(eventMsg{seq: 9, kind: "stream_activity", text: pair[0]})
		found := false
		for _, row := range chatRows(&m, 76) {
			if strings.Contains(row.text, pair[1]) {
				found = true
			}
		}
		if !found || len(m.Chat.Lines) != before || m.Chat.Stream.Len() != 0 {
			t.Fatalf("activity %q invisible or added to conversation", pair)
		}
	}
	m.onEvent(eventMsg{seq: 8, kind: "stream_activity", text: "thinking"})
	if m.Chat.StreamActivity != "" {
		t.Fatal("old run activity changed current display")
	}
	m.Chat.Running = false
	m.onEvent(eventMsg{seq: 9, kind: "stream_activity", text: "thinking"})
	if m.Chat.StreamActivity != "" {
		t.Fatal("completed run activity changed idle display")
	}
}
