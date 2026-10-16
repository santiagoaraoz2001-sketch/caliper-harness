package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

func TestRepairedCallUsesActualAuthorizationGate(t *testing.T) {
	reg := tool.NewRegistry()
	effects, approvals := 0, 0
	reg.Register(tool.Definition{Name: "read", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) { effects++; return tool.Result{}, nil }})
	m := &completedModel{reply: llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "one", Name: "inspect", Arguments: `{"path":"a.go"}`}}}}}
	r := &agent.Runner{Model: m, Registry: reg, MaxSteps: 1}
	r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		var packet struct{ Candidates []struct{ Name string } }
		if err := json.Unmarshal([]byte(req.Context), &packet); err != nil {
			t.Fatal(err)
		}
		for i, candidate := range packet.Candidates {
			if candidate.Name == "read" {
				return decision.Verdict{Pick: i + 1, Option: req.Options[i+1], Confidence: .9}, nil
			}
		}
		return decision.Verdict{Pick: 0, Option: req.Options[0], Confidence: .9}, nil
	}
	gate := auth.Gate{Mode: config.AuthAll, AskFunc: func(_ context.Context, prompt string) bool {
		approvals++
		if !strings.Contains(prompt, "read") || !strings.Contains(prompt, "a.go") {
			t.Fatal(prompt)
		}
		return false
	}}
	r.Exec = gate.Wrap(agent.Direct)
	_, _, err := r.Run(context.Background(), "inspect a.go")
	if err != nil || effects != 0 || approvals != 1 {
		t.Fatalf("effects %d approvals %d err %v", effects, approvals, err)
	}
}
