package workflowtools

import (
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Listing the library returns discovery summaries (identity, purpose,
// applies_to tags) without full block instructions or execution records,
// so checking the library at task start stays cheap as it grows. A tag
// filter narrows workflows to one task kind.
func TestLibraryListReturnsSummariesAndFiltersByTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	pc := prompts.Builtin()
	lib := LibraryWorkflow(path, pc)

	out := call(t, lib, tool.Args{"action": "list"})
	workflows, ok := out["workflows"].([]any)
	if !ok || len(workflows) == 0 {
		t.Fatalf("seed workflows missing from list: %v", out)
	}
	for _, raw := range workflows {
		w := raw.(map[string]any)
		if _, full := w["completion_requirements"]; full {
			t.Fatalf("list leaked full workflow body: %v", w)
		}
		if _, full := w["steps"]; full {
			t.Fatalf("list leaked workflow steps: %v", w)
		}
		if w["id"] == nil || w["purpose"] == nil {
			t.Fatalf("summary missing id/purpose: %v", w)
		}
	}
	blocks, ok := out["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("seed blocks missing from list: %v", out)
	}
	for _, raw := range blocks {
		b := raw.(map[string]any)
		if _, full := b["implementation"]; full {
			t.Fatalf("list leaked full block implementation: %v", b)
		}
	}

	out = call(t, lib, tool.Args{"action": "list", "tag": "playtest"})
	workflows = out["workflows"].([]any)
	if len(workflows) != 1 || workflows[0].(map[string]any)["id"] != "game-playtest-report" {
		t.Fatalf("tag filter wrong: %v", workflows)
	}
	if tags, ok := workflows[0].(map[string]any)["applies_to"].([]any); !ok || len(tags) == 0 || !strings.Contains(tags[0].(string), "playtest") {
		t.Fatalf("summary missing applies_to: %v", workflows[0])
	}
}
