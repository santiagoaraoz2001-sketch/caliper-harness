package workflowtools

import (
	"context"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func publishLensFixture(t *testing.T, path string) (tool.Definition, tool.Definition, tool.Definition) {
	t.Helper()
	pc := prompts.Builtin()
	bb, bw, lib := BuildBlock(path, pc), BuildWorkflow(path, pc), LibraryWorkflow(path, pc)
	out := call(t, bb, tool.Args{"action": "start", "id": "inspect-thing", "title": "Inspect thing"})
	rev := revision(out, "block")
	out = call(t, bb, tool.Args{"action": "set", "id": "inspect-thing", "expected_revision": rev, "fields": `{"purpose":"Inspect the target","inputs":"target","impl_type":"main_model","impl_instructions":"Read the target and record exact observations.","outputs":"observations","completion_evidence":"file paths and observed behavior","failure_handling":"stay in inspection","examples":"target=x => observations=y"}`})
	rev = revision(out, "block")
	call(t, bb, tool.Args{"action": "publish", "id": "inspect-thing", "expected_revision": rev})
	out = call(t, bw, tool.Args{"action": "start", "id": "inspect-flow", "title": "Inspect flow", "user_requested": true})
	rev = revision(out, "workflow")
	out = call(t, bw, tool.Args{"action": "set", "id": "inspect-flow", "expected_revision": rev, "fields": `{"purpose":"Inspect before changing","applies_to":"debug","inputs":"target","steps":"inspect-thing@1","completion_requirements":"observations recorded","revision_route":"return to inspection"}`})
	rev = revision(out, "workflow")
	call(t, bw, tool.Args{"action": "publish", "id": "inspect-flow", "expected_revision": rev})
	call(t, lib, tool.Args{"action": "enable", "id": "inspect-flow", "version": int64(1)})
	return bb, bw, lib
}

func TestLibraryWorkflowMethodLensAttachPhaseDetach(t *testing.T) {
	_, _, lib := publishLensFixture(t, filepath.Join(t.TempDir(), "library.json"))

	res, err := lib.Execute(context.Background(), tool.Args{
		"action": "attach", "id": "inspect-flow", "version": int64(1),
		"inputs": "target=thing", "adaptation": "Only inspect the public surface.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.MethodLens == nil || res.MethodLens.Mode != "attach" || res.MethodLens.Lens == nil {
		t.Fatalf("attach returned no lens update: %+v", res)
	}
	lens := res.MethodLens.Lens
	if lens.WorkflowID != "inspect-flow" || lens.WorkflowVersion != 1 || lens.StepIndex != 0 ||
		lens.BlockID != "inspect-thing" || lens.Instructions == "" || lens.Adaptation == "" {
		t.Fatalf("attached lens snapshot incomplete: %+v", lens)
	}

	res, err = lib.Execute(context.Background(), tool.Args{
		"action": "phase", "id": "inspect-flow", "version": int64(1), "step": int64(1),
		"observations": "target read", "risks": "none", "next_check": "configured check",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.MethodLens == nil || res.MethodLens.Mode != "phase" || res.MethodLens.Lens == nil ||
		res.MethodLens.Lens.Observations != "target read" || res.MethodLens.Lens.NextCheck != "configured check" {
		t.Fatalf("phase returned wrong lens update: %+v", res.MethodLens)
	}

	res, err = lib.Execute(context.Background(), tool.Args{"action": "detach"})
	if err != nil || res.MethodLens == nil || res.MethodLens.Mode != "detach" || res.MethodLens.Lens != nil {
		t.Fatalf("detach returned wrong lens update: %+v %v", res.MethodLens, err)
	}
	if _, err := lib.Execute(context.Background(), tool.Args{"action": "start", "id": "inspect-flow"}); err == nil {
		t.Fatal("retired execution-path action=start is still accepted")
	}
}
