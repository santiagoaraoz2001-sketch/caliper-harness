package auth

import (
	"context"
	"errors"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestDecisionDisabledRiskyAsksForExactAction(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "approve"}[accept], func(t *testing.T) {
			ran := 0
			var events []ApprovalEvent
			g := &Gate{Mode: config.AuthRisky, DecisionDisabled: true, Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
				t.Fatal("disabled judge called")
				return decision.Verdict{}, nil
			}, AskFunc: func(_ context.Context, prompt string) bool {
				if !strings.Contains(prompt, "Decision model disabled") || !strings.Contains(prompt, `"cmd": "echo exact"`) {
					t.Fatalf("missing exact approval: %s", prompt)
				}
				return accept
			}, Record: func(_ context.Context, e ApprovalEvent) error { events = append(events, e); return nil }}
			result, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "echo exact"})
			if err != nil || (ran == 1) != accept || result.Blocked == accept || len(events) != 1 || events[0].Approved != accept {
				t.Fatalf("approval mismatch ran%d result%+v err%v events%+v", ran, result, err, events)
			}
		})
	}
}
func TestDecisionDisabledRiskyCancelAndHeadlessDoNotRun(t *testing.T) {
	for _, cancelDuringAsk := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ran := 0
		g := &Gate{Mode: config.AuthRisky, DecisionDisabled: true}
		if cancelDuringAsk {
			g.AskFunc = func(context.Context, string) bool { cancel(); return true }
		}
		res, err := g.Wrap(counting(&ran))(ctx, fakeDef(), tool.Args{"cmd": "echo exact"})
		if ran != 0 || cancelDuringAsk && !errors.Is(err, context.Canceled) || !cancelDuringAsk && !res.Blocked {
			t.Fatalf("headless/cancel executed: %d %+v %v", ran, res, err)
		}
	}
}
