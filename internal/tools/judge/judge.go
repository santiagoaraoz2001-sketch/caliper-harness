// Package judge implements the on-call judge tool: the orchestrator asks
// the decision model a question mid-run. Free under authorize-risky —
// judging reads nothing and changes nothing.
package judge

import (
	"context"
	"fmt"
	"strings"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Deps carries the sidecar call plus the session hook. A nil Judge refuses
// every call; a nil Log skips the judgment event (sessionless runs).
type Deps struct {
	Judge           func(context.Context, decision.Request) (decision.Verdict, error)
	Log             func(kind, text string)
	MaxContextChars int
}

// New returns the judge definition: context to decide over, options as one
// comma-separated slot (mirroring the decision CLI), optional instructions
// (blank = the sidecar default).
func New(d Deps, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return tool.Definition{Prompts: pc,
		Name:    "judge",
		Label:   "judge",
		Desc:    pc.Text("tools.judge.description"),
		Purpose: pc.Text("tools.judge.purpose"),
		Params: []tool.Param{
			{Name: "context", Desc: pc.Text("tools.judge.parameters.context"), Type: tool.TypeString, Required: true},
			{Name: "options", Desc: pc.Text("tools.judge.parameters.options"), Type: tool.TypeString, Required: true},
			{Name: "instructions", Desc: pc.Text("tools.judge.parameters.instructions"), Type: tool.TypeString},
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
	if d.Judge == nil {
		return tool.Result{}, fmt.Errorf("judge tool is not wired — needs the decision sidecar")
	}
	optStr, _ := args["options"].(string)
	var opts []string
	for _, o := range strings.Split(optStr, ",") {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, o)
		}
	}
	if len(opts) < 2 {
		return tool.Result{}, fmt.Errorf("options names %d choice(s) — %s", len(opts), pc.Text("guidance.judge.options"))
	}
	contextText, _ := args["context"].(string)
	instructions, _ := args["instructions"].(string)
	req := decision.Request{Context: contextText, Instructions: instructions, Options: opts}
	v, err := d.Judge(ctx, req)
	if err != nil {
		if decision.EncodedCapacity(err) {
			return tool.Result{}, fmt.Errorf("%v — %s", err, pc.Text("guidance.judge.capacity"))
		}
		return tool.Result{}, fmt.Errorf("the decision sidecar refused or is unreachable (%v) — %s", err, pc.Text("guidance.judge.unreachable"))
	}
	decision.LogJudgment(d.Log, decision.SourceJudgeTool, req, v, "returned", d.MaxContextChars)
	return tool.Result{Text: fmt.Sprintf("pick: %s (confidence %.2f)", v.Option, v.Confidence)}, nil
}
