package agent

import (
	"context"
	"specific-harness/internal/decision"
	"strings"
	"testing"
)

func TestCapacityRecoveryBatchesThousandsOfOptionalItems(t *testing.T) {
	packet := struct {
		Intent   string
		Optional []string
	}{Intent: "required intent"}
	for i := 0; i < 4096; i++ {
		packet.Optional = append(packet.Optional, strings.Repeat("x", 80))
	}
	attempts := 0
	r := &Runner{JudgmentContextChars: 100}
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		attempts++
		if !strings.Contains(q.Context, "required intent") {
			t.Fatal("protected intent lost")
		}
		if len(q.Context) > 4096 {
			return decision.Verdict{}, &decision.ContextCapacityError{Message: "local byte admission"}
		}
		return decision.Verdict{Option: "continue"}, nil
	}
	_, _, err := r.progressJudgment(t.Context(), &packet, decision.Request{}, func() bool {
		if len(packet.Optional) == 0 {
			return false
		}
		packet.Optional = packet.Optional[:len(packet.Optional)-1]
		return true
	}, func(string, string) error { return nil })
	if err != nil || attempts > 9 || attempts < 2 {
		t.Fatalf("admission churn: calls=%d err=%v", attempts, err)
	}
}
