package agent_test

import (
	"context"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"testing"
)

func TestShellInspectionPreservesPairedCheck(t *testing.T) {
	r, check, _, dir := episodeRunner(t, "printf ACCEPTANCE")
	if _, err := check.Execute(context.Background(), tool.Args{"acceptance_cmd": "printf ACCEPTANCE", "cmd": r.CheckCommand, "timeout": int64(2)}); err != nil {
		t.Fatal(err)
	}
	c := r.Continuation.Coding
	if !c.Passed() {
		t.Fatal("fixture check not passed")
	}
	revision := c.Revision
	shell := bash.NewAt(config.Defaults().Tools.Bash, dir)
	if _, err := r.CodingExec(agent.Direct)(context.Background(), shell, tool.Args{"cmd": "ls .", "timeout": int64(2)}); err != nil {
		t.Fatal(err)
	}
	if !c.Passed() || c.Revision != revision {
		t.Fatalf("inspection invalidated check: %+v", c)
	}
	for _, cmd := range []string{"printf changed > source.js", "mv source.js renamed.js", "rm renamed.js"} {
		if _, err := check.Execute(context.Background(), tool.Args{"acceptance_cmd": "printf ACCEPTANCE", "cmd": r.CheckCommand, "timeout": int64(2)}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.CodingExec(agent.Direct)(context.Background(), shell, tool.Args{"cmd": cmd, "timeout": int64(2)}); err != nil {
			t.Fatal(err)
		}
		if c.Passed() {
			t.Fatalf("mutation retained check: %s", cmd)
		}
	}
}
