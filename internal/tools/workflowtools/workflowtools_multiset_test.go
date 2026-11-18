package workflowtools

import (
	"context"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// One set call must be able to fill every remaining field: the one-field-
// per-call contract cost ~500 trace events of authoring in c52 (one full
// model generation per field). A multi-field set advances the revision
// exactly once and applies atomically.
func TestBuildSetMultipleFieldsOneRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	pc := prompts.Builtin()
	bb, bw := BuildBlock(path, pc), BuildWorkflow(path, pc)

	out := call(t, bb, tool.Args{"action": "start", "id": "check-diff", "title": "Check diff"})
	startRev := revision(out, "block")
	out = call(t, bb, tool.Args{
		"action": "set", "id": "check-diff", "expected_revision": startRev,
		"fields": `{"purpose":"Run the declared check and record counts","inputs":"check command","impl_type":"tool","impl_tool":"check","outputs":"exit code and counts","completion_evidence":"the check result text","failure_handling":"record the failure and stop","examples":"npm run check => exit 0, 28/28"}`,
	})
	if got := revision(out, "block"); got != startRev+1 {
		t.Fatalf("multi-field set advanced revision %d -> %d, want exactly one step", startRev, got)
	}
	if missing, _ := out["missing"].([]any); len(missing) != 0 {
		t.Fatalf("fields still missing after multi-field set: %v", out["missing"])
	}
	rev := revision(out, "block")
	out = call(t, bb, tool.Args{"action": "publish", "id": "check-diff", "expected_revision": rev})
	if out["block"].(map[string]any)["status"] != "published" {
		t.Fatalf("block not published after multi-field set: %v", out)
	}

	out = call(t, bw, tool.Args{"action": "start", "id": "verified-change", "title": "Verified change", "user_requested": true})
	startRev = revision(out, "workflow")
	out = call(t, bw, tool.Args{
		"action": "set", "id": "verified-change", "expected_revision": startRev,
		"fields": `{"purpose":"Change then check","inputs":"check command","steps":"check-diff@1","completion_requirements":"check exit 0 with counts","revision_route":"return to the change with failure evidence"}`,
	})
	if got := revision(out, "workflow"); got != startRev+1 {
		t.Fatalf("workflow multi-field set advanced revision %d -> %d, want exactly one step", startRev, got)
	}
	rev = revision(out, "workflow")
	out = call(t, bw, tool.Args{"action": "publish", "id": "verified-change", "expected_revision": rev})
	if out["workflow"].(map[string]any)["status"] != "published" {
		t.Fatalf("workflow not published after multi-field set: %v", out)
	}
}

// A multi-field set is atomic: an invalid value refuses the whole call with
// nothing applied and no revision advance, and mixing the single-field and
// multi-field forms refuses as ambiguous.
func TestBuildSetMultipleFieldsAtomicAndUnambiguous(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	bb := BuildBlock(path, prompts.Builtin())
	out := call(t, bb, tool.Args{"action": "start", "id": "atomic", "title": "Atomic"})
	rev := revision(out, "block")

	if _, err := bb.Execute(context.Background(), tool.Args{
		"action": "set", "id": "atomic", "expected_revision": rev,
		"fields": `{"purpose":"must not land","impl_workflow":"no-version-marker"}`,
	}); err == nil {
		t.Fatal("invalid multi-field set accepted")
	}
	out = call(t, bb, tool.Args{"action": "get", "id": "atomic"})
	b := out["block"].(map[string]any)
	if b["purpose"] != "" || revision(out, "block") != rev {
		t.Fatalf("refused multi-field set partially applied: %v", b)
	}
	if _, err := bb.Execute(context.Background(), tool.Args{
		"action": "set", "id": "atomic", "expected_revision": rev,
		"field": "purpose", "value": "x", "fields": `{"purpose":"y"}`,
	}); err == nil {
		t.Fatal("field+fields mix accepted")
	}
	if _, err := bb.Execute(context.Background(), tool.Args{
		"action": "set", "id": "atomic", "expected_revision": rev,
		"fields": `{"bogus_field":"y"}`,
	}); err == nil {
		t.Fatal("unknown field in multi-field set accepted")
	}
	// Single-field set keeps working unchanged.
	out = call(t, bb, tool.Args{"action": "set", "id": "atomic", "field": "purpose", "value": "single still works", "expected_revision": rev})
	if revision(out, "block") != rev+1 {
		t.Fatalf("single-field set broken: %v", out)
	}
}
