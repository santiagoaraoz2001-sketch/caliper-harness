package launch

import (
	"context"
	"errors"
	"specific-harness/internal/decision"
	"strings"
	"testing"
)

func TestDecisionDisabledReadinessUsesExplicitHumanApproval(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "approve"}[accept], func(t *testing.T) {
			_, id, d := gateDeps(t, 1)
			d.ManualReadiness = true
			d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				t.Fatal("disabled judge called")
				return decision.Verdict{}, nil
			}
			var logged string
			d.Log = func(kind, text string) {
				if kind != "manual-readiness" {
					t.Fatalf("fabricated judge event: %s", kind)
				}
				logged = text
			}
			d.Ask = func(_ context.Context, prompt string) bool {
				if !strings.Contains(prompt, id) || !strings.Contains(prompt, "Decision model disabled") || !strings.Contains(prompt, "check") {
					t.Fatalf("incomplete readiness prompt: %s", prompt)
				}
				return accept
			}
			err := ReadyCheck(context.Background(), d, id)
			if (err == nil) != accept || !strings.Contains(logged, id) {
				t.Fatalf("approval mismatch err%v log%s", err, logged)
			}
		})
	}
}
func TestDecisionDisabledReadinessCancellationAndHeadlessRefuse(t *testing.T) {
	for _, cancelDuringAsk := range []bool{false, true} {
		_, id, d := gateDeps(t, 1)
		d.ManualReadiness = true
		d.In = nil
		d.Out = nil
		d.Ask = nil
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelDuringAsk {
			d.Ask = func(context.Context, string) bool { cancel(); return true }
		}
		err := ReadyCheck(ctx, d, id)
		if err == nil || cancelDuringAsk && !errors.Is(err, context.Canceled) {
			t.Fatalf("headless/cancel admitted: %v", err)
		}
	}
}
