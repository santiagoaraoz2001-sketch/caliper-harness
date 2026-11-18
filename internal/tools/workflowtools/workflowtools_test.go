package workflowtools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func call(t *testing.T, d tool.Definition, args tool.Args) map[string]any {
	t.Helper()
	res, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("%s %v: %v", d.Name, args, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		t.Fatalf("result not JSON: %q", res.Text)
	}
	return out
}

func revision(out map[string]any, key string) int64 {
	return int64(out[key].(map[string]any)["revision"].(float64))
}

func TestBuildPublishAttachThroughTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	pc := prompts.Builtin()
	bb, bw, lib := BuildBlock(path, pc), BuildWorkflow(path, pc), LibraryWorkflow(path, pc)

	out := call(t, bb, tool.Args{"action": "start", "id": "greet", "title": "Greet"})
	rev := revision(out, "block")
	for _, kv := range [][2]string{
		{"purpose", "Produce a greeting"}, {"inputs", "name"}, {"impl_type", "main_model"},
		{"impl_instructions", "Write one greeting line for the input name."}, {"outputs", "greeting"},
		{"completion_evidence", "the greeting text"}, {"failure_handling", "retry with the same input"},
		{"examples", "name=Ana => greeting=Hello Ana"},
	} {
		out = call(t, bb, tool.Args{"action": "set", "id": "greet", "field": kv[0], "value": kv[1], "expected_revision": rev})
		rev = revision(out, "block")
	}
	out = call(t, bb, tool.Args{"action": "publish", "id": "greet", "expected_revision": rev})
	if out["block"].(map[string]any)["status"] != "published" {
		t.Fatalf("block not published: %v", out)
	}

	out = call(t, bw, tool.Args{"action": "start", "id": "say-hello", "title": "Say hello", "user_requested": true})
	rev = revision(out, "workflow")
	for _, kv := range [][2]string{
		{"purpose", "Greet one person"}, {"inputs", "name"}, {"steps", "greet@1"},
		{"completion_requirements", "greeting recorded"}, {"revision_route", "return to greet with failure evidence"},
	} {
		out = call(t, bw, tool.Args{"action": "set", "id": "say-hello", "field": kv[0], "value": kv[1], "expected_revision": rev})
		rev = revision(out, "workflow")
	}
	call(t, bw, tool.Args{"action": "publish", "id": "say-hello", "expected_revision": rev})
	call(t, lib, tool.Args{"action": "enable", "id": "say-hello", "version": int64(1)})
	out = call(t, lib, tool.Args{"action": "attach", "id": "say-hello", "version": int64(1), "inputs": "name=Ana"})
	lens := out["method_lens"].(map[string]any)
	if out["current_block"] == nil || lens["workflow_id"] != "say-hello" || lens["step_index"] != float64(0) {
		t.Fatalf("method lens attach: %v", out)
	}
	out = call(t, lib, tool.Args{"action": "phase", "id": "say-hello", "version": int64(1), "step": int64(2), "observations": "greeting written"})
	if out["method_lens"].(map[string]any)["step_index"] != float64(-1) {
		t.Fatalf("method lens not complete: %v", out)
	}
}
