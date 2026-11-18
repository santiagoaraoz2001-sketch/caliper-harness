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

// A restored assignment for the same task and workspace routes task_start
// to the reclaim op with the recorded attempt; any other task uses Start.
func TestTaskStartReclaimsRestoredRunningAttempt(t *testing.T) {
	workspace := t.TempDir()
	for name, task := range map[string]string{"restored task": "task", "other task": "other"} {
		t.Run(name, func(t *testing.T) {
			r := &Runner{Prompts: prompts.Builtin(), Workspace: workspace, Session: &recordSink{}, SessionID: "reclaim", Outcome: outcome.Outcome{Status: outcome.Unknown}}
			r.Continuation.Version = 1
			r.Continuation.Add("intent", "user", "resume the running task", false)
			r.Continuation.Assignment = &session.Assignment{Store: "owner", Workspace: workspace, PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 5}
			used := ""
			r.Assigned = &AssignedOps{
				Start: func(context.Context, string) (*session.Assignment, string, error) {
					used = "start"
					return &session.Assignment{Store: "owner", Workspace: workspace, PlanID: "plan", ApproachID: "approach", TaskID: task, Attempt: 1}, "true", nil
				},
				Reclaim: func(_ context.Context, id string, attempt int) (*session.Assignment, string, error) {
					used = "reclaim"
					if id != "task" || attempt != 5 {
						return nil, "", errors.New("reclaim identity mismatch")
					}
					return &session.Assignment{Store: "owner", Workspace: workspace, PlanID: "plan", ApproachID: "approach", TaskID: id, Attempt: attempt}, "true", nil
				},
			}
			receipt, _, ok := r.invokeValidated(t.Context(), r.TaskStartDefinition(), tool.Args{"task": task}, Direct)
			if !ok {
				t.Fatalf("task_start: %s", receipt)
			}
			want := "reclaim"
			if task != "task" {
				want = "start"
			}
			if used != want || !r.assignedClaim {
				t.Fatalf("task_start used %q (want %q), claimed=%t", used, want, r.assignedClaim)
			}
		})
	}
}
