package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestFocusCannotHandOffUnsettledClaim(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		active, same, settled bool
	}{
		{name: "active different", active: true}, {name: "active same", active: true, same: true}, {name: "unclaimed"}, {name: "settled", active: true, settled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &session.Assignment{Store: "owner", Workspace: "/project", TaskID: "task-A", Attempt: 1}
			r := Runner{Prompts: prompts.Builtin(), Workspace: a.Workspace, Assigned: &AssignedOps{}, assignedClaim: tc.active, Outcome: outcome.Outcome{Status: outcome.Unknown}, Continuation: session.Continuation{Assignment: a, Coding: &session.CodingState{Workspace: a.Workspace, TaskID: a.TaskID, Attempt: 1}}}
			if tc.settled {
				r.settlement = &TaskSettlement{TaskID: a.TaskID, Qualified: true}
			}
			requested := &tool.WorkflowScope{Store: a.Store, Kind: "task", ID: "task-B"}
			if tc.same {
				requested.ID = a.TaskID
			}
			def := tool.Definition{Name: "workflow_focus", Execute: func(context.Context, tool.Args) (tool.Result, error) {
				return tool.Result{Focus: requested, Text: "workflow focus for next request: task " + requested.ID + "\nrequested page content"}, nil
			}}
			text, _, ok := r.invokeValidated(t.Context(), def, tool.Args{}, Direct)
			preserved := tc.active && !tc.settled && !tc.same
			expected := requested.ID
			if preserved {
				expected = a.TaskID
			}
			if !ok || r.pendingFocus == nil || r.pendingFocus.ID != expected {
				t.Fatalf("focus=%+v expected=%s output=%s", r.pendingFocus, expected, text)
			}
			if strings.Contains(text, "not a task handoff") != preserved || !strings.Contains(text, "requested page content") {
				t.Fatalf("incorrect receipt %s", text)
			}
			if preserved && (strings.Contains(text, "workflow focus for next request: task task-B") || !strings.Contains(text, "independent review") || !strings.Contains(text, "task-A")) {
				t.Fatalf("misleading handoff receipt: %s", text)
			}
			if r.Continuation.Assignment != a {
				t.Fatal("inspection reassigned task")
			}
		})
	}
}
