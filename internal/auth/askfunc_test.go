package auth

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

func TestAskFuncRisky(t *testing.T) {
	var asked []string
	g := &Gate{Mode: "authorize-risky", Threshold: 0.7,
		Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{Option: "allow", Confidence: 0.5}, nil
		},
		AskFunc: func(ctx context.Context, p string) bool { asked = append(asked, p); return true }}
	def := tool.Definition{Name: "bash"}
	next := func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		return tool.Result{Text: "ran"}, nil
	}
	res, err := g.Wrap(next)(context.Background(), def, tool.Args{})
	if err != nil || res.Text != "ran" || len(asked) != 1 || !strings.Contains(asked[0], "bash") {
		t.Errorf("res=%+v err=%v asked=%v", res, err, asked)
	}
}

func TestAskFuncDenies(t *testing.T) {
	g := &Gate{Mode: "authorize-all", AskFunc: func(context.Context, string) bool { return false }}
	def := tool.Definition{Name: "bash"}
	res, err := g.Wrap(nil)(context.Background(), def, tool.Args{})
	if err != nil || !res.Blocked {
		t.Errorf("res=%+v err=%v", res, err)
	}
}
