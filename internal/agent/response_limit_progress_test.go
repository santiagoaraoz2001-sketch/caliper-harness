package agent_test

import (
	"context"
	"encoding/json"
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

func TestResponseLimitUsesExistingD1ProgressJudgmentBeforeContinuation(t *testing.T) {
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
			var input struct {
				Checkpoint         agent.ProgressCheckpoint
				UnfinishedResponse struct{ Provenance, Field, Text string }
			}
			if err := json.Unmarshal([]byte(req.Context), &input); err != nil {
				t.Fatal(err)
			}
			if input.UnfinishedResponse.Field != "reasoning" || input.UnfinishedResponse.Text != "unfinished thought" || !strings.Contains(input.UnfinishedResponse.Provenance, "unverified partial") || input.Checkpoint.RunStep != 0 || len(req.Options) != 3 {
				t.Fatalf("length invented a final or charged model work: %s", req.Context)
			}
			return decision.Verdict{Option: req.Options[len(req.Options)-1], Confidence: .99}, nil
		}}
	_, _, err := r.Run(t.Context(), "original objective")
	if err != nil || r.Outcome.Status != outcome.Stopped || judgments != 1 || requests != 2 {
		t.Fatalf("progress nudge interrupted literal continuation or claimed verification: %v %+v judgments=%d requests=%d", err, r.Outcome, judgments, requests)
	}
}
