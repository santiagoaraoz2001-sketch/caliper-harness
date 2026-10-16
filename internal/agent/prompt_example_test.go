package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

// Exercise the real example given to the model, rather than a second copy.
func TestSystemPromptInspectionExampleExecutes(t *testing.T) {
	reg := tool.NewRegistry()
	workspace := &files.Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	for _, def := range workspace.Definitions() {
		reg.Register(def)
	}
	prompt := systemPrompt(reg)
	examples := 0
	for _, rest := range strings.Split(prompt, "\n```tool\n")[1:] {
		end := strings.Index(rest, "\n```")
		if end < 0 {
			t.Fatal("prompt example has no closing tool fence")
		}
		raw := "```tool\n" + rest[:end] + "\n```"
		call, found, repair := tool.Extract(raw)
		if !found || repair != "" {
			t.Fatalf("actual prompt example refuses: found=%v repair=%q raw=%q", found, repair, raw)
		}
		def, ok := reg.Lookup(call.Name)
		if !ok {
			t.Fatalf("example names unregistered tool %q", call.Name)
		}
		args, repair := tool.Validate(def, call.Slots)
		if repair != "" {
			t.Fatal(repair)
		}
		if call.Name != "read" || call.Slots["path"] != "." {
			t.Fatal("generic example demands an execution tool", call)
		}
		res, err := def.Execute(context.Background(), args)
		if err != nil || res.Failed || res.Truncated {
			t.Fatalf("actual inspection example: %+v %v", res, err)
		}
		examples++
	}
	if examples != 1 {
		t.Fatalf("want one compact executable inspection example, got %d", examples)
	}
}
