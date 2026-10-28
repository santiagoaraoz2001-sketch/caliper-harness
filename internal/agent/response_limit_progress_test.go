package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestResponseLimitContinuesWithoutD1ProgressJudgment(t *testing.T) {
	requests, judgments := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if requests > 1 {
			writeContinuationChunk(w, map[string]any{"content": "Provisional final, no claimed task or verification."}, "stop", nil, 20, 1)
			return
		}
		writeContinuationChunk(w, map[string]any{"reasoning": "unfinished thought"}, "length", map[string]any{"tokens": []int{1}, "raw_chunks": []string{"unfinished thought"}, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0}, 20, 1)
	}))
	defer srv.Close()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: tool.NewRegistry(), Session: &nativeRecordingSink{}, SessionID: "progress", MaxSteps: 1, Threshold: .7, ThinkingLoop: agent.ThinkingLoop{SampleBytes: 4096},
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owned-store", Kind: "root"}},
		Assigned: &agent.AssignedOps{Reset: func() {}, Facts: func(context.Context) (any, error) {
			return agent.BookkeepingFacts{Unfinished: []byte(`{"state":"queued"}`)}, nil
		}},
		Judge: func(_ context.Context, req decision.Request) (decision.Verdict, error) {
			if len(req.Options) != 3 {
				return decision.Verdict{Option: req.Options[0], Confidence: .99}, nil
			}
			judgments++
			t.Error("physical output limit invoked logical progress judgment")
			return decision.Verdict{Option: req.Options[len(req.Options)-1], Confidence: .99}, nil
		}}
	_, _, err := r.Run(t.Context(), "original objective")
	if err != nil || r.Outcome.Status != outcome.Completed || judgments != 0 || requests != 2 {
		t.Fatalf("progress nudge interrupted literal continuation or claimed verification: %v %+v judgments=%d requests=%d", err, r.Outcome, judgments, requests)
	}
}
