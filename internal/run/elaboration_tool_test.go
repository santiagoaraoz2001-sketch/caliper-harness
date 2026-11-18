package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

func TestElaborateToolCreatesAndRequestsDecision(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	node, err := launch.ScopeNode(f.d.Store, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	asked := ""
	f.d.Approve = func(_ context.Context, prompt string) bool {
		asked = prompt
		return true
	}
	def := elaborationDefinition(f.d, "fixture-session", f.d.Prompts)
	call := func(args tool.Args) tool.Result {
		t.Helper()
		raw, _ := json.Marshal(args)
		values, gap := tool.ValidateJSON(def, string(raw))
		if gap != "" {
			t.Fatalf("validate %v: %s\n%s", args, gap, def.Schema())
		}
		result, err := def.Execute(context.Background(), values)
		if err != nil || result.Failed {
			t.Fatalf("execute %v: %v %s", args, err, result.Text)
		}
		return result
	}

	result := call(tool.Args{
		"action": "set", "kind": "approach", "id": node.Parent,
		"purpose": "Deliver the loop", "inputs": "current source", "evidence": "focused regression and project check", "risks": "scope drift",
		"expected_previous_id": "", "request_decision": true,
	})
	var saved plan.Elaboration
	if err := json.Unmarshal([]byte(result.Text), &saved); err != nil {
		t.Fatalf("result is not an elaboration receipt: %v %s", err, result.Text)
	}
	if saved.Status != plan.ElaborationApproved || saved.Version != 1 || saved.SessionRef != "fixture-session" {
		t.Fatalf("saved = %+v", saved)
	}
	if result.Focus == nil || result.Focus.Kind != "approach" || result.Focus.ID != node.Parent {
		t.Fatalf("focus = %+v", result.Focus)
	}
	for _, want := range []string{"Deliver the loop", "focused regression", "scope drift", "version 1"} {
		if !strings.Contains(asked, want) {
			t.Errorf("decision prompt missing %q: %s", want, asked)
		}
	}
	got := call(tool.Args{"action": "get", "kind": "approach", "id": node.Parent})
	if !strings.Contains(got.Text, saved.ID) || !strings.Contains(got.Text, `"status":"approved"`) {
		t.Fatalf("get result = %s", got.Text)
	}
}
