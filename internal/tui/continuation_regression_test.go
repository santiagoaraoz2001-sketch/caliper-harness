package tui

import (
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestResumePreservesActualFailure(t *testing.T) {
	events := []session.Event{{Kind: "prompt", Text: "repair code"}, {Kind: "result", Text: "tool result:\nNO TESTS RAN\nexit5"}, {Kind: "error", Text: "model transport failed"}}
	h := historyFromEvents(events)
	var b strings.Builder
	for _, m := range h {
		b.WriteString(m.Content)
	}
	if !strings.Contains(b.String(), "NO TESTS RAN") || !strings.Contains(b.String(), "model transport failed") {
		t.Fatalf("resume erased actual failures:%s", b.String())
	}
}
