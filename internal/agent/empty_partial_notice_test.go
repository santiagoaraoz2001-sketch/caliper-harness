package agent_test

import (
	"bytes"
	"context"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestEmptyFailedNativeResponseDoesNotClaimReceivedContent(t *testing.T) {
	dir := t.TempDir()
	r, _, _ := failedNativeFixture(t, nil, thinkingLog{dir: dir})
	_, _, err := r.Run(context.Background(), "empty transport failure")
	if err == nil || r.Outcome.Status != outcome.Failed || !r.UsageUnknown {
		t.Fatalf("empty transport failure lost: %+v %v", r.Outcome, err)
	}
	text := r.Out.(*bytes.Buffer).String()
	if !strings.Contains(text, "response/error archive envelope") || !strings.Contains(text, "no model content or call draft received") || strings.Contains(text, "received thought/text retained") {
		t.Fatalf("misleading empty-response diagnostic: %s", text)
	}
	events, err := session.Read(dir, "partial-native")
	if err != nil {
		t.Fatal(err)
	}
	archived := false
	for _, e := range events {
		if e.Kind == "reasoning" {
			archived = true
		}
		if e.Kind == "native_reply" || e.Kind == "tool" || e.Kind == "native_result" || e.Kind == "done" {
			t.Fatalf("empty failed response gained content/effects/completion: %s", e.Kind)
		}
	}
	if !archived {
		t.Fatal("empty response/error evidence was not archived")
	}
}
