package agent_test

import (
	"context"
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
	const original = "retain the original objective"
	call := func(id, value string) map[string]any {
		return map[string]any{"index": 0, "id": id, "type": "function",
			"function": map[string]string{"name": "record", "arguments": value}}
	}
	var requests []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		switch len(requests) {
		case 1:
			writeContinuationChunk(w, map[string]any{"tool_calls": []any{call("settled", `{"name":"first"}`)}}, "tool_calls", nil, 90, 10)
		case 2:
			writeContinuationChunk(w, map[string]any{"reasoning": "unfinished thought", "tool_calls": []any{call("abandoned", `{"name":"incomp`)}}, "length", nil, 50035, 32768)
		default:
			writeContinuationChunk(w, map[string]any{"content": "finished"}, "stop", nil, 120, 4)
		}
	}))
	defer srv.Close()

	executions := 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Desc: "record a marker", Params: []tool.Param{{Name: "name", Type: tool.TypeString, Required: true}},
		Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
			executions++
			if args["name"] != "first" {
				t.Errorf("executed abandoned draft: %v", args)
			}
			return tool.Result{Text: "confirmed first marker"}, nil
		}})
	sink := &nativeRecordingSink{}
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("openai"), ModelKey: "fixture", Registry: reg, Session: sink, MaxSteps: 4, MaxTokens: 131072}
	final, _, err := r.Run(t.Context(), original)
	if err != nil || final != "finished" || r.Outcome.Status != outcome.Completed || len(requests) != 3 || executions != 1 {
		t.Fatalf("recovery: final=%q err=%v status=%s calls=%d effects=%d", final, err, r.Outcome.Status, len(requests), executions)
	}
	recovered := string(requests[2]["messages"])
	for _, field := range []string{original, "confirmed first marker", "output limit"} {
		if !strings.Contains(recovered, field) {
			t.Errorf("fresh request lost %q", field)
		}
	}
	if len(requests[2]["response_prefix"]) != 0 || strings.Contains(recovered, "unfinished thought") || strings.Contains(recovered, "abandoned") {
		t.Fatal("fresh request replayed an uncommitted response")
	}
	if r.UsedIn != 50245 || r.UsedOut != 32782 || r.UsageUnknown {
		t.Fatalf("usage lost or counted twice: %d/%d unknown=%t", r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
	var measured, archived bool
	var events []session.Event
	for _, e := range sink.events {
		events = append(events, session.Event{Kind: e.kind, Text: e.text})
		if e.kind == "context" {
			var reading session.Continuation
			if err := json.Unmarshal([]byte(e.text), &reading); err != nil {
				t.Fatal(err)
			}
			measured = measured || reading.Measured && reading.Prompt == 50035
		}
		if e.kind == "reasoning" && strings.Contains(e.text, "unfinished thought") && strings.Contains(e.text, "abandoned") {
			archived = true
		}
	}
	if !measured || !archived {
		t.Fatalf("length receipt: measured=%t archived=%t", measured, archived)
	}
	restored, _, err := session.Restore(events, 131072)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range restored.Messages() {
		for _, call := range m.ToolCalls {
			if call.ID == "abandoned" {
				t.Fatal("restoring session activated an abandoned tool call")
			}
		}
	}
}

func TestRepeatedOutputLimitHonorsStepBudget(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeContinuationChunk(w, map[string]any{"reasoning": "unfinished"}, "length", nil, 50, 20)
	}))
	defer srv.Close()
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("openai"), Registry: tool.NewRegistry(), Session: &nativeRecordingSink{}, MaxSteps: 2}
	r.Run(t.Context(), "work within two steps")
	if calls != 2 || r.Outcome.Status == outcome.Completed || r.UsedOut != 40 {
		t.Fatalf("calls=%d outcome=%+v output=%d", calls, r.Outcome, r.UsedOut)
	}
}
