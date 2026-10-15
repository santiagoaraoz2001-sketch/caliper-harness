package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Crossing the existing90% threshold invokes the actual model summary path;
// received drafts remain unverified and never become executable call history.
func TestLengthAtContextPressureCompactsAndContinuesFromFactualSummary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "owned-marker")
	requests, effects, summaries, progress, reviews := 0, 0, 0, 0, 0
	var fresh []llm.Message
	summary := "The original requirement is ALWAYS_REQ_ALPHA. The marker received one actual effect with receipt stable-effect. The assistant reasoning and pending native argument were unfinished unverified drafts, with no new effect."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		var body struct {
			Messages []llm.Message
			Prefix   json.RawMessage `json:"response_prefix"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch requests {
		case 1:
			writeContinuationChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "stable-effect", "type": "function", "function": map[string]string{"name": "effect", "arguments": `{"name":"marker"}`}}}}, "tool_calls", nil, 100, 1)
		case 2:
			thought := strings.Repeat("unfinished unverified code reasoning ", 300)
			writeContinuationChunk(w, map[string]any{"reasoning": thought}, "length", map[string]any{"tokens": []int{11}, "raw_chunks": []string{thought}, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0, "pending_tool_draft": "effect<arg_key>name</arg_key><arg_value>must-not-run"}, 400, 550)
		case 3:
			summaries++
			if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "unfinished unverified code reasoning") || !strings.Contains(body.Messages[1].Content, "must-not-run") || !strings.Contains(body.Messages[1].Content, "unverified") || len(body.Prefix) != 0 {
				t.Errorf("actual compactor did not receive nonexecuting original drafts: %+v", body)
			}
			writeContinuationChunk(w, map[string]any{"content": summary}, "stop", nil, 600, 20)
		case 4:
			fresh = body.Messages
			if len(body.Prefix) != 0 {
				t.Error("compacted prompt incorrectly retained old raw-prefix identity")
			}
			writeContinuationChunk(w, map[string]any{"content": "done from retained facts"}, "stop", nil, 650, 5)
		default:
			t.Error("unexpected repeated generation after compaction")
			writeContinuationChunk(w, map[string]any{"content": "extra"}, "stop", nil, 1, 1)
		}
	}))
	defer srv.Close()
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "effect", Params: []tool.Param{{Name: "name", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
		if args["name"] != "marker" {
			t.Error("unfinished pending argument became an effect")
		}
		effects++
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return tool.Result{}, err
		}
		defer file.Close()
		_, err = file.WriteString("one actual effect\n")
		return tool.Result{Text: "actual marker write settled"}, err
	}})
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: reg, Session: &nativeRecordingSink{}, SessionID: "pressure", MaxSteps: 2, MaxTokens: 1000, Threshold: .7, ThinkingLoop: agent.ThinkingLoop{SampleBytes: 4096}, Compaction: agent.Compaction{Trigger: 90, Keep: 50}, Continuation: session.Continuation{Version: 1, BasisTokens: 100, BasisChars: 100000, Entries: []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "ALWAYS_REQ_ALPHA: preserve original requirement and actual effect receipt"}}}}, Judge: func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		if len(req.Options) == 3 {
			progress++
			if !strings.Contains(req.Context, "unverified partial") || !strings.Contains(req.Context, "unfinished unverified code reasoning") {
				t.Error("length handover did not review actual unfinished thinking")
			}
			return decision.Verdict{Option: req.Options[0], Confidence: .99}, nil
		}
		reviews++
		return decision.Verdict{Option: req.Options[0], Confidence: .99}, nil
	}}
	final, _, err := r.Run(t.Context(), "perform one marker effect, then retain ALWAYS_REQ_ALPHA")
	if err != nil || r.Outcome.Status != outcome.Completed || final != "done from retained facts" || effects != 1 || summaries != 1 || progress != 1 || reviews != 1 || requests != 4 || r.Continuation.Compactions != 1 {
		t.Fatalf("pressure handover failed: final=%q err=%v outcome=%+v effects=%d summary=%d progress=%d review=%d requests=%d compactions=%d", final, err, r.Outcome, effects, summaries, progress, reviews, requests, r.Continuation.Compactions)
	}
	bytes, readErr := os.ReadFile(marker)
	if readErr != nil || string(bytes) != "one actual effect\n" {
		t.Fatal("completed effect repeated/lost", fmt.Sprintf("%q", bytes), readErr)
	}
	all := ""
	for _, m := range fresh {
		all += m.Content + "\n"
	}
	if !strings.Contains(all, "ALWAYS_REQ_ALPHA") || !strings.Contains(all, summary) || !strings.Contains(all, "stable-effect") {
		t.Fatalf("factual summary lost requirements/effect identity: %s", all)
	}
	for _, m := range fresh {
		for _, call := range m.ToolCalls {
			if strings.Contains(call.Arguments, "must-not-run") {
				t.Fatal("incomplete arguments reentered executable call history")
			}
		}
	}
	if err := r.Continuation.Validate(); err != nil {
		t.Fatal(err)
	}
}
