package tools

import (
	"io"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

func TestHelpReadNavigationAliases(t *testing.T) {
	cfg := config.Defaults()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	reg := tool.NewRegistry()
	var active *tool.Registry
	RegisterAt(reg, cfg, nil, io.Discard, nil, w, func() *tool.Registry { return active })
	active = reg.View([]string{"help", "read"})
	for _, topic := range []string{"read.definition", "help.read.outline"} {
		r, err := helpCall(t, active, topic)
		if err != nil || !strings.Contains(r.Text, "## help."+strings.TrimPrefix(topic, "help.")) || !strings.Contains(r.Text, "Current schema:") || !strings.Contains(r.Text, "tool: read") || !strings.Contains(r.Text, "Go") {
			t.Fatalf("focused alias %s: %+v %v", topic, r, err)
		}
		if _, exists := reg.Lookup(strings.TrimPrefix(topic, "help.")); exists {
			t.Fatal("help alias registered a new callable")
		}
	}
	active = reg.View([]string{"help"})
	if res, err := helpCall(t, active, "read.definition"); err != nil || !strings.Contains(res.Text, "unavailable in the current tool view") || strings.Contains(res.Text, "Current schema:") {
		t.Fatal("alias did not explain unavailable read honestly", res, err)
	}
	if _, err := helpCall(t, active, "read.unknown"); err == nil {
		t.Fatal("unknown subtopic accepted")
	}
}
