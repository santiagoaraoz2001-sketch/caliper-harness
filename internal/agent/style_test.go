package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
)

func styleRunner(judge JudgeFunc) *Runner {
	return &Runner{Prompts: prompts.Builtin(), Judge: judge,
		StyleEnforcement: true, Styleguide: "Prefer early returns.",
		settlement: &TaskSettlement{TaskID: "t1", Attempt: 1, Verification: "passed"}}
}

func TestStyleSendbackOnlyOnConfidentConflictOnce(t *testing.T) {
	log := func(string, string) error { return nil }
	sendback := styleRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: styleOptions[1], Confidence: .9}, nil
	})
	guidance := sendback.styleSendback(context.Background(), "done with nested conditionals", log)
	if !strings.Contains(guidance, "Prefer early returns") || !strings.Contains(guidance, "STYLE SEND-BACK") {
		t.Fatalf("send-back guidance missing styleguide: %q", guidance)
	}
	if again := sendback.styleSendback(context.Background(), "adjusted", log); again != "" {
		t.Fatalf("second send-back round ran: %q", again)
	}
	for name, judge := range map[string]JudgeFunc{
		"approve": func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{Option: styleOptions[0], Confidence: .9}, nil
		},
		"low confidence": func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{Option: styleOptions[1], Confidence: .2}, nil
		},
		"judge timeout": func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{}, errors.New("timed out")
		},
		"unsupported": func(context.Context, decision.Request) (decision.Verdict, error) {
			return decision.Verdict{Option: "rewrite it", Confidence: .9}, nil
		},
		"disabled by nil": nil,
	} {
		r := styleRunner(judge)
		if got := r.styleSendback(context.Background(), "done", log); got != "" {
			t.Fatalf("%s blocked a validated handoff: %q", name, got)
		}
	}
	off := styleRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: styleOptions[1], Confidence: .99}, nil
	})
	off.StyleEnforcement = false
	if got := off.styleSendback(context.Background(), "done", log); got != "" {
		t.Fatalf("disabled style enforcement judged anyway: %q", got)
	}
}
