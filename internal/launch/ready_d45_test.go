package launch

import (
	"context"
	"specific-harness/internal/decision"
	"strings"
	"testing"
)

func TestD45ReadinessAsksBoundedBehavior(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		if len(q.Options) != 2 || !strings.Contains(q.Options[0], "individually bounded") || !strings.Contains(q.Options[0], "clear behavior") || !strings.Contains(q.Options[0], "own acceptance assertions") || !strings.Contains(q.Options[1], "too broad") || !strings.Contains(q.Options[1], "future task") || !strings.Contains(q.Options[1], "acceptance assertions") || !strings.Contains(q.Instructions, "clear behavior to implement and verify") {
			t.Errorf("D45 predicate absent: %+v", q)
		}
		return decision.Verdict{Option: q.Options[1], Confidence: 1}, nil
	}
	if err := ReadyCheck(context.Background(), d, aid); err == nil {
		t.Fatal("negative statement must refuse")
	}
}
