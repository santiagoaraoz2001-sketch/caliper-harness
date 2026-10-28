package agent_test

import (
	"context"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"testing"
)

func TestCheckCannotInheritOwnedServiceActions(t *testing.T) {
	r, _, _, dir := episodeRunner(t, "printf behavior")
	owner := &bash.ServiceOwner{}
	defer owner.Close()
	owned := bash.NewAtOwned(config.Defaults().Tools.Bash, dir, owner)
	original := owned.Execute
	effects := 0
	owned.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) { effects++; return original(ctx, args) }
	check := r.CheckDefinition(owned)
	for _, p := range check.Params {
		if p.Name == "service" {
			t.Error("check advertised persistent service actions")
		}
	}
	if args, repair := tool.ValidateJSON(check, `{"service":"stop"}`); repair == "" || args != nil {
		t.Error("check schema accepted service action")
	}
	for _, action := range []string{"start", "status", "stop", ""} {
		_, err := check.Execute(t.Context(), tool.Args{"cmd": r.CheckCommand, "acceptance_cmd": "printf behavior", "service": action})
		if err == nil || effects != 0 || r.Continuation.Coding.Receipt != nil {
			t.Fatalf("service %q reached check effects/receipt: effects=%d err=%v", action, effects, err)
		}
	}
	args, repair := tool.ValidateJSON(check, `{}`)
	if repair != "" {
		t.Fatal(repair)
	}
	result, err := check.Execute(t.Context(), args)
	if err != nil || result.Failed || !r.Continuation.Coding.Passed() || effects != 2 {
		t.Fatalf("ordinary pair lost synchronous completion: effects=%d result=%+v err=%v", effects, result, err)
	}
}
