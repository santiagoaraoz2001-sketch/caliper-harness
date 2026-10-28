package agent

import (
	"context"
	"errors"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestDirectClaimMovesApproachFocusOnlyOnSuccess(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		r := &Runner{Prompts: prompts.Builtin(), Workspace: t.TempDir(), Session: &recordSink{}, SessionID: "claim", Outcome: outcome.Outcome{Status: outcome.Unknown}}
		r.Continuation.Version = 1
		r.Continuation.Add("intent", "user", "implement selected task", false)
		r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "approach", ID: "approach"}
		r.Assigned = &AssignedOps{Start: func(context.Context, string) (*session.Assignment, string, error) {
			if refuse {
				return nil, "", errors.New("claim refused")
			}
			return &session.Assignment{Store: "owner", Workspace: r.Workspace, PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}, "true", nil
		}}
		receipt, _, ok := r.invokeValidated(t.Context(), r.TaskStartDefinition(), tool.Args{"task": "task"}, Direct)
		if !refuse && !ok {
			t.Fatalf("claim: %s", receipt)
		}
		if err := r.settleFocus(func(string, string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if refuse {
			if r.Continuation.Workflow.Kind != "approach" || r.assignedClaim {
				t.Fatal("failed claim changed focus")
			}
		} else if !r.ClaimedTaskScope(r.Continuation.Workflow) {
			t.Fatal("successful claim left tools in nonexecuting scope")
		}
	}
}
