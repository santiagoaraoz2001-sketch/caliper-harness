package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestPATClaimedFrozenBriefAtSystemRole(t *testing.T) {
	brief := map[string]string{"title": "Fix entrypoint", "prompt": "Implement precise requested behavior", "current": "observed old behavior", "target": "distinctive frozen target", "sketch": "one grounded change", "acceptance": "distinguishing assertion", "tests": "real assertion", "verify": "inspect responsible source", "check_command": "project-check", "acceptance_command": "behavior-check"}
	a := &session.Assignment{Store: "owner", Workspace: "/project", TaskID: "current-task", Attempt: 1}
	r := Runner{Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), Workspace: "/project", Assigned: &AssignedOps{ClaimedBrief: func() map[string]string { return brief }}, assignedClaim: true, Continuation: session.Continuation{Assignment: a, Coding: &session.CodingState{TaskID: a.TaskID, Attempt: a.Attempt, Workspace: a.Workspace}}, Outcome: outcome.Outcome{Status: outcome.Unknown}}
	r.begin("execute requested work")
	first := r.Continuation.Entries[0]
	if first.Message.Role != "system" || !strings.Contains(first.Message.Content, brief["target"]) || !strings.Contains(first.Message.Content, brief["prompt"]) || !strings.Contains(first.Message.Content, brief["acceptance_command"]) {
		t.Fatalf("actual frozen task absent from system instructions: %s", first.Message.Content)
	}
	if !strings.Contains(first.Message.Content, "independent review") || !strings.Contains(first.Message.Content, "next task") {
		t.Fatal("brief has no stable review/handoff paragraph")
	}
	r.assignedClaim = false
	if strings.Contains(r.basePrompt(), brief["target"]) {
		t.Fatal("historical assignment installed task authority")
	}
	r.assignedClaim = true
	r.settlement = &TaskSettlement{TaskID: a.TaskID, Attempt: 1, State: "done", Qualified: true}
	if strings.Contains(r.basePrompt(), brief["target"]) {
		t.Fatal("settled task retained execution instructions")
	}
}

func TestPATNativeScopeAndCodingArrayRefusesBeforeEffects(t *testing.T) {
	for _, name := range []string{"workflow_plan", "workflow_approach", "workflow_task", "workflow_focus"} {
		t.Run(name, func(t *testing.T) {
			for _, claimed := range []bool{false, true} {
				calls := 0
				reg := tool.NewRegistry()
				for _, n := range []string{name, "write"} {
					reg.Register(tool.Definition{Name: n, Execute: func(context.Context, tool.Args) (tool.Result, error) { calls++; return tool.Result{}, nil }})
				}
				r := Runner{Prompts: prompts.Builtin(), Registry: reg, Assigned: &AssignedOps{}, assignedClaim: claimed, Continuation: session.Continuation{Version: 1}}
				r.Continuation.Add("intent", "user", "current work", false)
				message := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "scope", Name: name, Arguments: `{}`}, {ID: "write", Name: "write", Arguments: `{}`}}}
				_, _, e := r.nativeStep(context.Background(), message, Direct, func(string, string) error { return nil }, nil, func(string, bool, string) string { return "" })
				if e != nil || calls != 0 {
					t.Fatalf("selection and coding effects were mixed at claimed=%t: calls=%d err=%v", claimed, calls, e)
				}
			}
		})
	}
}
