package agent

import (
	"context"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"testing"
)

// Mechanical route-name parity only; outside-tool execution belongs to its owner.
func TestOutsideDiscoveryPreparationParity(t *testing.T) {
	for _, name := range []string{"read_outside", "find_outside"} {
		t.Run(name, func(t *testing.T) {
			pc, e := prompts.Default()
			if e != nil {
				t.Fatal(e)
			}
			reg := tool.NewRegistry(pc)
			reg.Register(tool.Definition{Name: name})
			r := &Runner{Prompts: pc, Registry: reg, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil }}}
			if e := r.refreshWorkflow(t.Context()); e != nil {
				t.Fatal(e)
			}
			if _, ok := r.Registry.Lookup(name); !ok {
				t.Fatal("discovery route absent before claim")
			}
			r.enforcement.repair = "pending PAT"
			r.bookkeepingProgress(t.Context(), true)
			r.enforcement.current = &observed{Tool: name, Result: tool.Result{Text: "new actual source receipt"}}
			if !r.bookkeepingProgress(t.Context(), true) {
				t.Fatal("fresh discovery not counted")
			}
			if r.bookkeepingProgress(t.Context(), true) {
				t.Fatal("repeat counted as fresh")
			}
			r.enforcement.current.Result.NoMatches = true
			if r.bookkeepingProgress(t.Context(), true) {
				t.Fatal("empty result counted")
			}
		})
	}
}
