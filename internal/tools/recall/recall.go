// Package recall implements the on-call recall tool: the model asks
// past memories a question mid-run. Free under authorize-risky —
// reading memories changes nothing.
package recall

import (
	"context"
	"fmt"

	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// Deps carries the store plus the recall caps (TopN 0 disables,
// MaxChars 0 lifts the char cap, MinWord 0 keeps every word).
type Deps struct {
	Store       *store.Store
	ProjectRoot string // captured by the host; blank retains legacy global recall
	TopN        int
	MaxChars    int
	MinWord     int
}

// New returns the recall definition: a query plus an optional kind
// filter (event, decision, note; blank searches all).
func New(d Deps, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return tool.Definition{Prompts: pc,
		Name:    "recall",
		Label:   "recall",
		Desc:    pc.Text("tools.recall.description"),
		Purpose: pc.Text("tools.recall.purpose"),
		Params: []tool.Param{
			{Name: "query", Desc: pc.Text("tools.recall.parameters.query"), Type: tool.TypeString, Required: true},
			{Name: "kind", Desc: pc.Text("tools.recall.parameters.kind"), Type: tool.TypeString, AllowEmpty: true, ExactEnum: true, Enum: []string{"", "event", "decision", "note"}},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			return run(ctx, d, args, pc)
		},
	}
}

func run(ctx context.Context, d Deps, args tool.Args, catalogs ...*prompts.Catalog) (tool.Result, error) {
	pc := prompts.Select(catalogs...)
	if d.Store == nil {
		return tool.Result{}, fmt.Errorf("recall tool is not wired — needs the store")
	}
	query, _ := args["query"].(string)
	kind, _ := args["kind"].(string)
	switch kind {
	case "", "event", "decision", "note":
	default:
		return tool.Result{}, fmt.Errorf("memory kind must be one of %s, got %q", memory.ValidKinds, kind)
	}
	var block string
	var err error
	if d.ProjectRoot != "" {
		block, err = memory.QueryProject(ctx, d.Store, d.ProjectRoot, kind, query, d.TopN, d.MaxChars, d.MinWord, pc)
	} else {
		block, err = memory.Query(d.Store, kind, query, d.TopN, d.MaxChars, d.MinWord, pc)
	}
	if err != nil {
		return tool.Result{}, fmt.Errorf("cannot read memories: %v", err)
	}
	if block != "" {
		return tool.Result{Text: block}, nil
	}
	return tool.Result{Text: fmt.Sprintf("no matching memories available within recall budget for %q", query)}, nil
}
