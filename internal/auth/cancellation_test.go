package auth

import (
	"context"
	"errors"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

func TestCancellationApprovalAndDecisionCannotAdmitTool(t *testing.T) {
	for _, mode := range []string{config.AuthAll, config.AuthRisky} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		gate := &Gate{Mode: mode, Threshold: 0.5,
			AskFunc: func(context.Context, string) bool { cancel(); return true },
			Judge: func(got context.Context, _ decision.Request) (decision.Verdict, error) {
				if got != ctx {
					t.Fatal("judgment context was substituted")
				}
				cancel()
				return decision.Verdict{Option: "allow", Confidence: 1}, nil
			},
		}
		_, err := gate.Wrap(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
			calls++
			return tool.Result{}, nil
		})(ctx, tool.Definition{Name: "bash"}, nil)
		cancel()
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("%s admitted after cancellation: calls=%d err=%v", mode, calls, err)
		}
	}
}
