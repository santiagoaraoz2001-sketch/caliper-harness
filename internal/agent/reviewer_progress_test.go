package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
)

func TestCapturedReviewerProgressUsesReviewRoleAndPreservesSilence(t *testing.T) {
	raw, err := os.ReadFile("testdata/reviewer-repetition.json")
	if err != nil {
		t.Fatal(err)
	}
	var captured progressPacket
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	for _, choice := range []struct {
		pick       int
		confidence float64
		nudge      bool
	}{{0, .881, false}, {2, .499, false}, {2, .9, true}} {
		r := &Runner{Prompts: prompts.Builtin(), Reviewing: true, TaskSystem: captured.AuthoredTaskInstructions, Session: &recordSink{}, SessionID: "review-fixture"}
		r.ThinkingLoop.SampleBytes = captured.UnfinishedResponse.OriginalBytes
		judged := false
		r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
			judged = true
			var packet progressPacket
			if err := json.Unmarshal([]byte(req.Context), &packet); err != nil {
				t.Fatal(err)
			}
			if packet.Checkpoint.Phase != "REVIEW" || strings.Contains(packet.Checkpoint.NextAction, "implement the") || !strings.Contains(packet.Checkpoint.NextAction, "return PASS") || packet.AuthoredTaskInstructions != captured.AuthoredTaskInstructions || !strings.Contains(packet.UnfinishedResponse.Text, "loadBest") {
				t.Fatalf("wrong reviewer packet: %+v", packet)
			}
			return decision.Verdict{Option: req.Options[choice.pick], Confidence: choice.confidence}, nil
		}
		unfinished := llm.Message{Role: "assistant", Reasoning: captured.UnfinishedResponse.Text}
		before := unfinished.Reasoning
		advice, err := r.reviewDiscovery(t.Context(), &watcher{}, func(string, string) error { return nil }, unfinished)
		if err != nil || !judged || (advice != "") != choice.nudge || unfinished.Reasoning != before {
			t.Fatalf("advice/silence changed: %q %v", advice, err)
		}
		if choice.nudge && (!strings.Contains(advice, "specific missing fact") || !strings.Contains(advice, "return PASS")) {
			t.Fatalf("unfocused reviewer nudge: %s", advice)
		}
	}
}
