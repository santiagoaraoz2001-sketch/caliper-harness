package agent_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestOutputLimitPromptMeasurementWithoutContinuationPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeContinuationChunk(w, map[string]any{"reasoning": "unfinished thought"}, "length", nil, 47684, 32768)
	}))
	defer srv.Close()
	sink := &nativeRecordingSink{}
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), ModelKey: "fixture", Registry: tool.NewRegistry(), Session: sink, MaxTokens: 262144}
	_, _, err := r.Run(t.Context(), "retain the original objective")
	if err == nil || !strings.Contains(err.Error(), "literal token-prefix state") || r.Outcome.Status != outcome.Failed {
		t.Fatalf("measurement changed missing-prefix refusal: %v %+v", err, r.Outcome)
	}
	if !r.Continuation.Measured || r.Continuation.Prompt != 47684 || r.UsedIn != 47684 || r.UsedOut != 32768 || r.UsageUnknown {
		t.Fatalf("valid reported prompt lost or counted twice: prompt=%d measured=%t totals=%d/%d unknown=%t", r.Continuation.Prompt, r.Continuation.Measured, r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
	var latest session.Continuation
	for _, event := range sink.events {
		if event.kind == "context" {
			if err := json.Unmarshal([]byte(event.text), &latest); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !latest.Measured || latest.Prompt != 47684 || latest.ModelKey != "fixture" {
		t.Fatalf("measurement did not reach existing context event: %+v", latest)
	}
}
