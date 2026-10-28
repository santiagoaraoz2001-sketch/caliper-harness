package agent_test

import (
	"context"
	"errors"
	"specific-harness/internal/tool"
	"testing"
)

func TestPairedChecksKeepIndependentFailureEvidence(t *testing.T) {
	for _, mode := range []string{"exit", "launch-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, _, _, _ := episodeRunner(t, "behavior")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls []string
			bash := tool.Definition{Name: "bash", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
				cmd := a["cmd"].(string)
				calls = append(calls, cmd)
				if cmd == "behavior" {
					if mode == "cancel" {
						cancel()
					}
					if mode == "launch-error" {
						return tool.Result{}, errors.New("behavior runtime missing")
					}
					code := 1
					return tool.Result{ExitCode: &code, Failed: true, Text: "behavior failed"}, nil
				}
				code := 0
				return tool.Result{ExitCode: &code, Text: "broad suite passed"}, nil
			}}
			def := r.CheckDefinition(bash)
			args, repair := tool.ValidateJSON(def, `{}`)
			if repair != "" {
				t.Fatal(repair)
			}
			res, err := def.Execute(ctx, args)
			if mode == "cancel" {
				if len(calls) != 1 || err == nil {
					t.Fatalf("cancellation launched suite: %v %v", calls, err)
				}
				return
			}
			if len(calls) != 2 || calls[1] != r.CheckCommand {
				t.Fatalf("independent suite suppressed: %v", calls)
			}
			c := r.Continuation.Coding
			if c.Passed() || !res.Failed || c.Receipt.Status != "passed" || c.Receipt.Acceptance.Status == "passed" {
				t.Fatalf("lost independent outcome: %+v %+v", res, c.Receipt)
			}
			if mode == "launch-error" && err == nil {
				t.Fatal("lost acceptance error")
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
