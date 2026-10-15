package files_test

import (
	"context"
	"io"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	builtins "specific-harness/internal/tools"
	"specific-harness/internal/tools/files"
	"strings"
	"testing"
)

func TestOutsideDiscoveryRegisteredHelpAndAuthDefaults(t *testing.T) {
	cfg := config.Defaults()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	reg := tool.NewRegistry()
	builtins.RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	view := reg.View(agent.CodingTools)
	help, ok := view.Lookup("help")
	if !ok {
		t.Fatal("missing help")
	}
	for _, name := range []string{"read_outside", "find_outside"} {
		d, ok := view.Lookup(name)
		if !ok {
			t.Fatal("missing standard tool", name)
		}
		a, repair := tool.Validate(help, map[string]string{"topic": name})
		if repair != "" {
			t.Fatal(repair)
		}
		res, err := help.Execute(context.Background(), a)
		if err != nil || !strings.Contains(res.Text, "## help."+name) || !strings.Contains(res.Text, "absolute") || !strings.Contains(res.Text, "path") {
			t.Fatalf("%s help=%+v err=%v", name, res, err)
		}
		if _, repair := tool.Validate(d, map[string]string{}); repair == "" {
			t.Fatal("outside path not required", name)
		}
		for _, free := range cfg.Auth.FreeTools {
			if free == name {
				t.Fatalf("%s silently became free", name)
			}
		}
	}
	a, repair := tool.Validate(help, map[string]string{"topic": "guide"})
	if repair != "" {
		t.Fatal(repair)
	}
	res, err := help.Execute(context.Background(), a)
	if err != nil || !strings.Contains(res.Text, "help.read_outside") || !strings.Contains(res.Text, "help.find_outside") {
		t.Fatalf("full help %+v %v", res, err)
	}
	for _, name := range []string{"edit_outside", "write_outside", "grep_outside"} {
		if _, ok := reg.Lookup(name); ok {
			t.Fatal("unexpected capability", name)
		}
	}
}
