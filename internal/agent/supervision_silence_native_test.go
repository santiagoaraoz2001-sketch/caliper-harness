package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Real Runner/client/SSE. Responses are explicit transport doubles; this is not
// model-performance evidence. The model owns its uninterrupted literal prefix.
func TestSupervisionNativeContinuationPreservesPrefixAndSilence(t *testing.T) {
	for _, observeThinking := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			pick       int
			confidence float64
			nudge      bool
		}{
			{"continue confident", 0, .99, false}, {"continue failed", 0, .499, false},
			{"act failed", 1, .499, false}, {"reconsider failed", 2, .404, false},
			{"act floor", 1, .5, true}, {"reconsider successful", 2, .6, true},
		} {
			name := tc.name
			if observeThinking {
				name += " streamed judgment"
			}
			t.Run(name, func(t *testing.T) {
				requests := []map[string]json.RawMessage{}
				effects, judgments := 0, 0
				thought := strings.Repeat("ongoing useful reasoning ", 4000)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests = append(requests, body)
					switch len(requests) {
					case 1:
						raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning": thought}}}})
						fmt.Fprintf(w, "data: %s\n\n", raw)
						w.(http.Flusher).Flush()
						writeContinuationChunk(w, map[string]any{}, "length", map[string]any{"tokens": []int{11, 12}, "raw_chunks": []string{thought[:len(thought)/2], thought[len(thought)/2:]}, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0}, 40, 32768)
					case 2:
						if effects != 0 {
							t.Error("unfinished response executed an effect")
						}
						writeContinuationChunk(w, map[string]any{"reasoning": "and now execute", "tool_calls": []any{map[string]any{"index": 0, "id": "effect-one", "type": "function", "function": map[string]string{"name": "effect", "arguments": `{}`}}}}, "tool_calls", nil, 32808, 5)
					case 3:
						writeContinuationChunk(w, map[string]any{"content": "finished"}, "stop", nil, 32820, 1)
					default:
						t.Error("unexpected forced response restart")
						writeContinuationChunk(w, map[string]any{"content": "extra"}, "stop", nil, 1, 1)
					}
				}))
				defer srv.Close()
				reg := tool.NewRegistry()
				reg.Register(tool.Definition{Name: "effect", Desc: "component effect", Execute: func(context.Context, tool.Args) (tool.Result, error) {
					effects++
					return tool.Result{Text: "one actual component effect"}, nil
				}})
				r := &agent.Runner{Prompts: prompts.Builtin(), Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: reg,
					Session: &nativeRecordingSink{}, SessionID: "literal-prefix", MaxSteps: 2, Threshold: .7,
					ThinkingLoop: agent.ThinkingLoop{SampleBytes: 4096}, Judge: func(_ context.Context, q decision.Request) (decision.Verdict, error) {
						judgments++
						pick := tc.pick
						if observeThinking && len(q.Options) == 3 {
							t.Error("physical output limit invoked progress judgment")
							pick = 0
						}
						if len(q.Options) == 2 {
							pick = 0
							if tc.pick == 0 {
								pick = 1
							}
						}
						return decision.Verdict{Pick: pick, Option: q.Options[pick], Confidence: tc.confidence}, nil
					}}
				if observeThinking {
					r.ThinkingLoop = agent.ThinkingLoop{Repeat: 16, MinUnitBytes: 8, MaxUnitBytes: 256, SampleBytes: 4096, MaxInputBytes: 8192}
				}
				final, _, err := r.Run(t.Context(), "execute once after completing your own reasoning")
				wantJudgments := 0 // Output limits are transparent physical boundaries.
				if observeThinking {
					wantJudgments++
				}
				if err != nil || final != "finished" || r.Outcome.Status != outcome.Completed || len(requests) != 3 || effects != 1 || judgments != wantJudgments {
					t.Fatalf("advice changed execution/continuation: %v final=%q outcome=%+v requests=%d effects=%d judgments=%d", err, final, r.Outcome, len(requests), effects, judgments)
				}
				if r.UsedIn != 65668 || r.UsedOut != 32774 || r.UsageUnknown {
					t.Fatalf("supervision lost or double-counted continuation usage: in=%d out=%d unknown=%t", r.UsedIn, r.UsedOut, r.UsageUnknown)
				}
				if string(requests[1]["messages"]) != string(requests[0]["messages"]) || len(requests[1]["response_prefix"]) == 0 {
					t.Fatal("judge changed context or discarded the literal unfinished prefix")
				}
				var prefix llm.ResponsePrefix
				if err := json.Unmarshal(requests[1]["response_prefix"], &prefix); err != nil || len(prefix.Tokens) != 2 || prefix.Tokens[0] != 11 || prefix.Tokens[1] != 12 || len(prefix.RawChunks) != 2 || strings.Join(prefix.RawChunks, "") != thought {
					t.Fatalf("prefix changed: %+v %v", prefix, err)
				}
				var messages []llm.Message
				if err := json.Unmarshal(requests[2]["messages"], &messages); err != nil {
					t.Fatal(err)
				}
				nudges, retained := 0, false
				for _, msg := range messages {
					if msg.Role == "assistant" && msg.Reasoning == thought+"and now execute" {
						retained = true
					}
					if msg.Role == "user" && msg.Content != "execute once after completing your own reasoning" {
						nudges++
						if strings.TrimSpace(msg.Content) == "" {
							t.Error("empty guidance message")
						}
					}
				}
				wantNudge := tc.nudge && observeThinking // Existing stream observation still applies independently.
				if !retained || (nudges > 0) != wantNudge || r.Continuation.Assignment != nil {
					t.Fatalf("fresh boundary retained=%t nudges=%d wanted=%t", retained, nudges, tc.nudge)
				}
			})
		}
	}
}
