package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exercise the real example given to the model, rather than a second copy.
func TestSystemPromptMultilineExampleExecutes(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(bash.New(config.Defaults().Tools.Bash))
	prompt := systemPrompt(reg)
	var scalar, multiline bool
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
		if !strings.Contains(call.Slots["cmd"], "\n") {
			scalar = true
			continue
		}
		multiline = true
		res, err := def.Execute(context.Background(), args)
		if err != nil || res.Failed || res.Truncated || res.Text != "OK\n" {
			t.Fatalf("actual multiline prompt example: result=%+v err=%v", res, err)
		}
		t.Logf("actual parsed cmd=%q; registered Bash output=%q", call.Slots["cmd"], res.Text)
	}
	if !scalar || !multiline {
		t.Fatalf("prompt lacks executable examples: scalar=%v multiline=%v", scalar, multiline)
	}
}
