package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRulithPlanningPlacementKeepsLogicalIdentityAndLiteralContinuation(t *testing.T) {
	tools := rulithTestTools()
	plan := tools[0]
	plan.Function.Name = "plan"
	plan.Function.Description = "current owner task-a; observations required"
	plan.TailPublished = true
	tools = append(tools, plan)
	msgs := []Message{{Role: "system", Content: "latest host plan question owns fields"}, {Role: "user", Content: "host plan task-a: observations required"}}
	renders, completions := 0, 0
	prompt := "retained history\n<|im_start|>assistant\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/apply-template":
			renders++
			var wire []ToolDefinition
			json.Unmarshal(body["tools"], &wire)
			if len(wire) != 1 || wire[0].Function.Name != "record" || strings.Contains(string(body["tools"]), "TailPublished") {
				t.Fatalf("placement leaked or lost stable tools: %s", body["tools"])
			}
			json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
		case "/completion":
			var got string
			json.Unmarshal(body["prompt"], &got)
			if got != prompt+strings.Repeat("partial é", completions) {
				t.Fatalf("literal prefix changed: %q", got)
			}
			completions++
			w.Header().Set("Content-Type", "text/event-stream")
			nativeEvent(w, map[string]any{"content": "partial é", "model": "qwen"})
			nativeEvent(w, nativeTerminal(got, "limit", 8))
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "qwen").WithRuntime(RuntimeRulith)
	prior, err := c.ChatTools(context.Background(), msgs, tools, nil)
	if err == nil || prior.Prefix == nil {
		t.Fatal("expected literal boundary", err)
	}
	for _, change := range []string{"placement", "hidden-schema"} {
		alter := append([]ToolDefinition(nil), tools...)
		if change == "placement" {
			alter[1].TailPublished = false
		} else {
			alter[1].Function.Description = "new question"
		}
		if _, err = c.ChatTools(WithResponseContinuation(context.Background(), prior), msgs, alter, nil); err == nil {
			t.Fatal("stale continuation accepted", change)
		}
	}
	if renders != 1 || completions != 1 {
		t.Fatal("mismatch reached server")
	}
	_, err = c.ChatTools(WithResponseContinuation(context.Background(), prior), msgs, tools, nil)
	if err == nil || renders != 1 || completions != 2 {
		t.Fatal("exact continuation did not reuse prefix", err)
	}
	if len(tools) != 2 || tools[1].Function.Name != "plan" {
		t.Fatal("logical schema mutated")
	}
}

func TestRulithOnlyPlanKeepsTemplateToolFraming(t *testing.T) {
	tools := rulithTestTools()
	tools[0].Function.Name = "plan"
	tools[0].TailPublished = true
	prompt := "<|im_start|>assistant\n"
	renders := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apply-template" {
			renders++
			var body chatRequest
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Tools) != 1 || body.Tools[0].Function.Name != "plan" {
				t.Fatal("only-tool framing disappeared")
			}
			json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		nativeEvent(w, map[string]any{"content": "done", "model": "qwen"})
		nativeEvent(w, nativeTerminal(prompt, "eos", 1))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "qwen").WithRuntime(RuntimeRulith).ChatTools(context.Background(), []Message{{Role: "user", Content: "open planning"}}, tools, nil)
	if err != nil || renders != 1 {
		t.Fatal(err, renders)
	}
}
