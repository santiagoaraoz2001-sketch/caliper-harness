package auth

import (
	"context"
	"errors"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"testing"
)

func TestLifecycleContractCacheAndChangedSnapshot(t *testing.T) {
	stamp, brief := "one", "complete original"
	asks, records := 0, 0
	g := &Gate{Mode: config.AuthRisky, AskFunc: func(context.Context, string) bool { asks++; return asks == 1 }, Record: func(_ context.Context, e ApprovalEvent) error {
		records++
		if e.Snapshot != stamp || e.Brief != brief {
			t.Fatal("incomplete authority event")
		}
		return nil
	}}
	load := func(context.Context) (string, string, error) { return stamp, brief, nil }
	if e := g.ApproveContract(t.Context(), "task", "actual", load); e != nil {
		t.Fatal(e)
	}
	if e := g.ApproveContract(t.Context(), "task", "actual", load); e != nil || asks != 1 || records != 1 {
		t.Fatal("first-start authority not retained", e, asks, records)
	}
	stamp, brief = "two", "complete revised"
	if e := g.ApproveContract(t.Context(), "task", "actual", load); e == nil || asks != 2 || records != 2 {
		t.Fatal("changed contract reused old authority", e, asks, records)
	}
}
func TestLifecycleContractChangesDuringQuestionRefuse(t *testing.T) {
	stamp := "one"
	g := &Gate{Mode: config.AuthRisky, AskFunc: func(context.Context, string) bool { stamp = "two"; return true }}
	if e := g.ApproveContract(t.Context(), "task", "actual", func(context.Context) (string, string, error) { return stamp, "complete actual contract", nil }); e == nil {
		t.Fatal("changed approved snapshot admitted")
	}
}
func TestLifecycleRecordingFailureWithholdsActualEffect(t *testing.T) {
	effects := 0
	want := errors.New("actual sink unavailable")
	g := &Gate{Mode: config.AuthAll, AskFunc: func(context.Context, string) bool { return true }, Record: func(context.Context, ApprovalEvent) error { return want }}
	_, e := g.Wrap(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{}, nil
	})(t.Context(), tool.Definition{Name: "actual"}, tool.Args{"cmd": "complete exact action"})
	if !errors.Is(e, want) || effects != 0 {
		t.Fatal("permission escaped failed durable recording", e, effects)
	}
}

func TestLifecycleActionChangedDuringHumanQuestionWithholdsEffect(t *testing.T) {
	args := tool.Args{"cmd": "approved original"}
	effects := 0
	g := &Gate{Mode: config.AuthAll, AskFunc: func(context.Context, string) bool { args["cmd"] = "unapproved changed"; return true }}
	result, err := g.Wrap(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{}, nil
	})(t.Context(), tool.Definition{Name: "actual"}, args)
	if err != nil || !result.Blocked || effects != 0 {
		t.Fatal("changed exact action retained authority", result, err, effects)
	}
}
