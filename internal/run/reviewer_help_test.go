package run

import (
	"io"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
	"strings"
	"testing"
)

func TestReviewerHelpUsesRestrictedActualFactory(t *testing.T) {
	cfg := config.Defaults()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	d := launchDeps(cfg, nil, "http://unused", "unused", 0, 32768, strings.NewReader(""), io.Discard, w, "")
	reg := d.NewRegistry()
	reg.Register(tool.Definition{Name: "check", Desc: "test check"})
	reg.Restrict([]string{"read", "read_outside", "find_outside", "grep", "find_file", "find_dir", "help", "bash", "check"})
	for _, name := range []string{"edit", "write", "graphql", "plan", "workflow_task"} {
		if _, ok := reg.Lookup(name); ok {
			t.Fatalf("reviewer advertises %s", name)
		}
	}
	help, _ := reg.Lookup("help")
	for _, topic := range []string{"schema", "schema.edit", "guide"} {
		res, err := help.Execute(t.Context(), tool.Args{"topic": topic})
		if topic == "schema.edit" {
			if err == nil && strings.Contains(res.Text, "tool: edit") {
				t.Fatal("schema leaks editing")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Text, "tool: edit") || strings.Contains(res.Text, "tool: write") || strings.Contains(res.Text, "## help.edit\n") {
			t.Fatalf("help leaks editing schema: %s", topic)
		}
		if !strings.Contains(res.Text, "check") {
			t.Fatal("lost check documentation")
		}
	}
}
