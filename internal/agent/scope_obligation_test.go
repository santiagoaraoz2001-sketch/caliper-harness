package agent

import (
	"context"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestInspectionDoesNotAdoptHistoricalCodingObligation(t *testing.T) {
	for _, historical := range []bool{false, true} {
		r := &Runner{Prompts: prompts.Builtin(), Model: &scriptModel{replies: []string{"Here is the requested inspection."}}, Registry: tool.NewRegistry(), Session: &recordSink{}, SessionID: "inspection", CodingRequired: true, CodingOnEffects: true, Workspace: t.TempDir(), CheckCommand: "must not execute"}
		if historical {
			r.Continuation.Coding = &session.CodingState{TaskID: "old", Workspace: r.Workspace, Attempt: 1, Revision: 7}
		}
		calls := 0
		r.RetainHostCheck(tool.Definition{Name: "check", Execute: func(context.Context, tool.Args) (tool.Result, error) { calls++; return tool.Result{}, nil }})
		_, _, err := r.Run(context.Background(), "Inspect the saved plan. No implementation or tests.")
		if err != nil || r.Outcome.Status != outcome.Completed || calls != 0 {
			t.Fatalf("historical=%v outcome=%+v checks=%d err=%v", historical, r.Outcome, calls, err)
		}
		if historical && (r.Continuation.Coding.Closed || r.Continuation.Coding.TaskID != "old" || r.Continuation.Coding.Revision != 7) {
			t.Fatalf("inspection changed prior execution contract: %+v", r.Continuation.Coding)
		}
	}
}

func TestProjectEffectActivatesCodingButNoEffectDoesNot(t *testing.T) {
	for _, changed := range []bool{false, true} {
		r := &Runner{Prompts: prompts.Builtin(), CodingRequired: true, CodingOnEffects: true, Workspace: t.TempDir()}
		d := tool.Definition{Name: "write", Execute: func(context.Context, tool.Args) (tool.Result, error) { return tool.Result{Changed: changed}, nil }}
		_, err := r.CodingExec(Direct)(context.Background(), d, tool.Args{})
		// A missing durable sink correctly refuses recording after the effect, but
		// cannot erase its verification obligation.
		if changed != r.CodingObligation() {
			t.Fatalf("changed=%v obligation=%v err=%v", changed, r.CodingObligation(), err)
		}
		if !changed && r.Continuation.Coding != nil {
			t.Fatal("no-effect write adopted coding state")
		}
	}
}
