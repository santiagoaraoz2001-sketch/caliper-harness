package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func writeContinuationChunk(w http.ResponseWriter, delta any, reason string, prefix any, in, out int) {
	row := map[string]any{"model": "fixture", "choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": in, "completion_tokens": out}}
	if prefix != nil {
		row["response_prefix"] = prefix
	}
	raw, _ := json.Marshal(row)
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

// Real HTTP/SSE transport, Runner validation/settlement and filesystem effects.
// The fixed model is a component oracle, not native inference qualification.
func TestOutputLimitContinuationsRetainPrefixAndExecuteOnce(t *testing.T) {
	dir := t.TempDir()
	unfinished := strings.Repeat("unfinished thought ", 3000) // Large, but still live; pruning must preserve the literal prefix.
	requests := []map[string]json.RawMessage{}
	call := func(index int, id, name, args string) map[string]any {
		v := map[string]any{"index": index, "function": map[string]string{"arguments": args}}
		if id != "" {
			v["id"], v["type"] = id, "function"
			v["function"].(map[string]string)["name"] = name
		}
		return v
	}
	prefix := func(tokens []int, chunks []string) any {
		return map[string]any{"tokens": tokens, "raw_chunks": chunks, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		switch len(requests) {
		case 1:
			writeContinuationChunk(w, map[string]any{"reasoning": unfinished}, "length", prefix([]int{11}, []string{unfinished}), 40, 3)
		case 2:
			writeContinuationChunk(w, map[string]any{"reasoning": "now acting", "tool_calls": []any{call(0, "stable-one", "effect", `{"name":"fi`)}}, "length", prefix([]int{11, 12}, []string{unfinished, "now acting<tool>{\"name\":\"fi"}), 45, 4)
		case 3:
			writeContinuationChunk(w, map[string]any{"tool_calls": []any{call(0, "", "", `rst"}`), call(1, "stable-two", "effect", `{"name":"second"}`)}}, "tool_calls", nil, 50, 5)
		case 4:
			writeContinuationChunk(w, map[string]any{"content": "done"}, "stop", nil, 60, 6)
		default:
			t.Error("unexpected repeated request/effect")
			writeContinuationChunk(w, map[string]any{"content": "extra"}, "stop", nil, 1, 1)
		}
	}))
	defer srv.Close()
	reg := tool.NewRegistry()
	var effects []string
	reg.Register(tool.Definition{Name: "effect", Desc: "write an owned marker", Params: []tool.Param{{Name: "name", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
		name := args["name"].(string)
		effects = append(effects, name)
		err := os.WriteFile(filepath.Join(dir, name), []byte("one actual effect"), 0600)
		return tool.Result{Text: "settled " + name}, err
	}})
	sink := &nativeRecordingSink{}
	r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithRuntime("mlx"), Registry: reg, Session: sink, SessionID: "owned", MaxSteps: 3, Prune: agent.Prune{OutputsAgeTokens: 50000}}
	projected := 0
	r.ProjectContext = func(_ context.Context, c *session.Continuation, _ string) error {
		projected++
		c.Add("evidence", "user", fmt.Sprintf("current project identity version %d", projected), false)
		return nil
	}
	final, _, err := r.Run(t.Context(), "retain original objective")
	if err != nil || r.Outcome.Status != outcome.Completed || final != "done" || !reflect.DeepEqual(effects, []string{"first", "second"}) || len(requests) != 4 {
		t.Fatalf("continuation/effects final=%q err=%v outcome=%+v effects=%v requests=%d", final, err, r.Outcome, effects, len(requests))
	}
	if projected != 2 {
		t.Fatalf("project context refreshed during unfinished response: %d", projected)
	}
	for _, name := range effects {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(raw) != "one actual effect" {
			t.Fatalf("effect bytes %s: %q %v", name, raw, err)
		}
	}
	for i := 1; i <= 2; i++ {
		if string(requests[i]["messages"]) != string(requests[0]["messages"]) || string(requests[i]["tools"]) != string(requests[0]["tools"]) || len(requests[i]["response_prefix"]) == 0 {
			t.Fatalf("length request %d changed original context or omitted literal prefix: %s", i+1, requests[i])
		}
	}
	if r.UsedIn != 195 || r.UsedOut != 18 || r.UsageUnknown {
		t.Fatalf("handover cost was lost/doubled: %d/%d unknown=%t", r.UsedIn, r.UsedOut, r.UsageUnknown)
	}
	var messages []llm.Message
	if err := json.Unmarshal(requests[3]["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	n := len(messages)
	if n < 4 || messages[n-4].Reasoning != unfinished+"now acting" || len(messages[n-4].ToolCalls) != 2 || messages[n-3].ToolCallID != "stable-one" || messages[n-2].ToolCallID != "stable-two" || messages[n-1].Content != "current project identity version 2" {
		t.Fatalf("assembled thought/call/result association lost: %+v", messages)
	}
}
