package agent

import (
	"context"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestDecisionDisabledSkipsAdviceButKeepsCodingAdmission(t *testing.T) {
	r := &Runner{ManualDecisions: true, pendingFocus: &session.WorkflowScope{Store: "actual", Kind: "plan", ID: "new-plan"}, Assigned: &AssignedOps{}}
	effects := 0
	_, err := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{}, nil
	})(t.Context(), tool.Definition{Name: "write"}, tool.Args{})
	if err == nil || !strings.Contains(err.Error(), "selected work has no ordinary project coding admission") || effects != 0 {
		t.Fatalf("decision opt-out bypassed owning admission: effects%d err%v", effects, err)
	}
	if advice, err := r.enforce(t.Context(), true, "final", func(string, string) error { t.Fatal("disabled advice attempted judgment logging"); return nil }); err != nil || advice != "" {
		t.Fatalf("disabled advice not skipped: %s %v", advice, err)
	}
}
