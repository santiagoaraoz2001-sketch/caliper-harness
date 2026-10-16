package agent

import (
	"encoding/json"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestPreparationPromptStableAndQuestionAtTail(t *testing.T) {
	pc := prompts.Builtin()
	reg := tool.NewRegistry(pc)
	question := "First planning question"
	r := &Runner{Prompts: pc, Registry: reg, Assigned: &AssignedOps{}, Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "store", Kind: "plan", ID: "one"}}}
	r.Continuation.Add("injected", "system", "", false)
	r.WorkflowView = func(*session.WorkflowScope) (*tool.Registry, string, error) {
		view := tool.NewRegistry(pc)
		view.Register(tool.Definition{Name: "plan", Prompts: pc, Purpose: question})
		return view, "Selected workflow: " + question, nil
	}
	if err := r.refreshWorkflow(); err != nil {
		t.Fatal(err)
	}
	first := r.Continuation.Entries[0].Message.Content
	if strings.Contains(first, question) {
		t.Fatal("current question leaked into system prefix")
	}
	n := len(r.Continuation.Entries)
	if err := r.refreshWorkflow(); err != nil {
		t.Fatal(err)
	}
	if len(r.Continuation.Entries) != n {
		t.Fatal("unchanged question duplicated")
	}
	question = "Second planning question"
	if err := r.refreshWorkflow(); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.Entries[0].Message.Content != first {
		t.Fatal("preparation system prompt changed")
	}
	if !strings.Contains(r.Continuation.Entries[len(r.Continuation.Entries)-1].Message.Content, question) {
		t.Fatal("current question not supplied")
	}
	question = "Third planning question"
	def := tool.Definition{Name: "plan", Prompts: pc, Purpose: question}
	receipt, _ := json.Marshal(map[string]string{"next_question": def.Schema()})
	r.Continuation.Add("output", "user", string(receipt), false)
	n = len(r.Continuation.Entries)
	if err := r.refreshWorkflow(); err != nil {
		t.Fatal(err)
	}
	if len(r.Continuation.Entries) != n {
		t.Fatal("current receipt question was duplicated")
	}
	if !preparationTool("plan") {
		t.Fatal("plan receipt cannot replace stale preparation refusal")
	}
}
