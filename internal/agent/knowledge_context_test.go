package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestCodingModesShareKnowledgeWithoutGuideBodies(t *testing.T) {
	for _, mode := range []string{"ordinary", "claimed", "guided"} {
		claimed := mode == "claimed"
		r := Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), CodingRequired: true, Workspace: t.TempDir(), Outcome: outcome.Outcome{Status: outcome.Unknown}}
		if mode == "guided" {
			r.Assigned = &AssignedOps{}
			r.Continuation.Workflow = &session.WorkflowScope{}
		}
		if claimed {
			a := &session.Assignment{Store: "owner", Workspace: r.Workspace, TaskID: "task", Attempt: 1}
			r.Assigned = &AssignedOps{}
			r.assignedClaim = true
			r.Continuation.Assignment = a
			r.Continuation.Coding = &session.CodingState{TaskID: a.TaskID, Attempt: a.Attempt, Workspace: a.Workspace}
		}
		got := r.basePrompt()
		if strings.Count(got, r.Prompts.Text("system.coding.knowledge")) != 1 {
			t.Fatalf("mode=%s: shared knowledge instruction missing or duplicated", mode)
		}
		for _, body := range []string{"## typography", "## boundaries", "prepareTask(input:", "fresh paired"} {
			if strings.Contains(got, body) {
				t.Fatalf("claimed=%t injected %s", claimed, body)
			}
		}
	}
}
