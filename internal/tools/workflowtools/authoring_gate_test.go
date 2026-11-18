package workflowtools

import (
	"context"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestWorkflowAuthoringSearchForkAndRevisionApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	pc := prompts.Builtin()
	bw := BuildWorkflow(path, pc)

	if _, err := bw.Execute(context.Background(), tool.Args{"action": "start", "id": "custom-ui", "title": "Custom UI"}); err == nil {
		t.Fatal("workflow creation accepted without a library search or explicit user request")
	}
	list := call(t, bw, tool.Args{"action": "list"})
	token, _ := list["search_token"].(string)
	if token == "" {
		t.Fatalf("list returned no search token: %v", list)
	}
	out := call(t, bw, tool.Args{"action": "start", "id": "custom-ui-design", "title": "UI element design", "search_token": token})
	similar, _ := out["similar_workflows"].([]any)
	foundSeed := false
	for _, raw := range similar {
		foundSeed = foundSeed || raw.(map[string]any)["id"] == "ui-element-design"
	}
	if !foundSeed {
		t.Fatalf("similar workflow not surfaced before authoring: %v", out)
	}

	out = call(t, bw, tool.Args{"action": "fork", "id": "custom-ui-element", "title": "Custom UI element", "source_id": "ui-element-design"})
	forked := out["workflow"].(map[string]any)
	if forked["based_on"] != "ui-element-design@1" || forked["status"] != "draft" {
		t.Fatalf("fork provenance/status wrong: %v", forked)
	}
	rev := revision(out, "workflow")
	out = call(t, bw, tool.Args{"action": "publish", "id": "custom-ui-element", "expected_revision": rev})
	out = call(t, bw, tool.Args{"action": "edit", "id": "custom-ui-element"})
	rev = revision(out, "workflow")
	missing, _ := out["missing"].([]any)
	if len(missing) == 0 {
		t.Fatalf("edited workflow did not require revision fields: %v", out)
	}
	out = call(t, bw, tool.Args{"action": "set", "id": "custom-ui-element", "expected_revision": rev,
		"fields": `{"revision_reason":"The user was not satisfied with the spacing outcome","revision_kind":"preference"}`})
	rev = revision(out, "workflow")
	if _, err := bw.Execute(context.Background(), tool.Args{"action": "publish", "id": "custom-ui-element", "expected_revision": rev}); err == nil {
		t.Fatal("preference revision published without user approval")
	}
	out = call(t, bw, tool.Args{"action": "publish", "id": "custom-ui-element", "expected_revision": rev, "user_approved": true})
	if out["workflow"].(map[string]any)["version"] != float64(2) || out["revision_notice"] == nil || out["revision_notice"] == "" {
		t.Fatalf("approved preference revision not published with notice: %v", out)
	}
}
