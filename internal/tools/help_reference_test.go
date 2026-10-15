package tools

import (
	"io"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

// Actual registered help stays readable without registering the documented
// capability or copying an unavailable schema into the model's tool view.
func TestHelpReferencesAndUnavailableDocumentation(t *testing.T) {
	cfg := config.Defaults()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	reg := tool.NewRegistry()
	var active *tool.Registry
	RegisterAt(reg, cfg, nil, io.Discard, nil, w, func() *tool.Registry { return active })
	active = reg.View([]string{"help"})
	for _, topic := range []string{"read", "read.definition", "launch", "plan"} {
		res, err := helpCall(t, active, topic)
		if err != nil || !strings.Contains(res.Text, "unavailable in the current tool view") || strings.Contains(res.Text, "Current schema:") {
			t.Fatalf("documented unavailable %s: %s %v", topic, res.Text, err)
		}
	}
	for _, topic := range []string{"reference", "reference.pat", "reference.workers", "reference.scope", "reference.tools"} {
		res, err := helpCall(t, active, topic)
		if err != nil || !strings.Contains(res.Text, "Reference") || strings.Contains(res.Text, "Current schema:") {
			t.Fatalf("selected reference %s: %s %v", topic, res.Text, err)
		}
		if topic != "reference" && strings.Contains(res.Text, "## Reference index") {
			t.Fatalf("selected document injected index: %s", res.Text)
		}
	}
	if names := active.Names(); len(names) != 1 || names[0] != "help" {
		t.Fatalf("reading guidance expanded executable scope: %v", names)
	}
	for _, topic := range []string{"absent", "reference.absent", "read.unknown", "compact_index"} {
		if _, err := helpCall(t, active, topic); err == nil {
			t.Fatalf("unknown topic %s accepted", topic)
		}
	}
	all, err := helpCall(t, active, "")
	if err != nil || !strings.Contains(all.Text, "## Reference index") || strings.Contains(all.Text, "## Reference: PAT lifecycle") {
		t.Fatalf("broad help did not keep references selected: %s %v", all.Text, err)
	}
	active = reg.View([]string{"help", "read"})
	read, err := helpCall(t, active, "read.definition")
	if err != nil || !strings.Contains(read.Text, "available in the current tool view") || !strings.Contains(read.Text, "Current schema:") || !strings.Contains(read.Text, "tool: read") {
		t.Fatalf("active alias lacks actual schema: %s %v", read.Text, err)
	}
}

func TestHelpReferenceUsesSelectedCatalog(t *testing.T) {
	cfg := config.Defaults()
	cfg.PromptCatalog = selectedCatalog(t)
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash, Prompts: cfg.PromptCatalog}
	reg := tool.NewRegistry(cfg.PromptCatalog)
	RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	res, err := helpCall(t, reg.View([]string{"help"}), "reference.workers")
	if err != nil || !strings.Contains(res.Text, "[selected catalog]") {
		t.Fatalf("reference lost editable snapshot: %s %v", res.Text, err)
	}
}
