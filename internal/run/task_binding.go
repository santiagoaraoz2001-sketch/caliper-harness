package run

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/agent"
	"specific-harness/internal/graph"
	"specific-harness/internal/tool"
)

// bindPreparedTask retains only a complete typed creation receipt in this run.
// It is neither workflow focus nor an execution claim, and is never restored
// from historical prose or a graph-wide "latest task" query.
func bindPreparedTask(r *agent.Runner, prepare, ready, start tool.Definition) (tool.Definition, tool.Definition, tool.Definition) {
	type receipt struct{ task, scope, digest string }
	var selected *receipt
	ambiguous := false
	scope := func() string {
		raw, _ := json.Marshal(r.Continuation.Workflow)
		return r.Assigned.Store + "\n" + r.Workspace + "\n" + r.SessionID + "\n" + string(raw)
	}
	reset := r.Assigned.Reset
	r.Assigned.Reset = func() { reset(); selected = nil; ambiguous = false }
	observe := r.Assigned.Observe
	r.Assigned.Observe = func(d tool.Definition, a tool.Args, v tool.Result, e error) {
		if observe != nil {
			observe(d, a, v, e)
		}
		if v.Focus != nil {
			selected = nil
			ambiguous = true
		}
	}
	bindPrepare := prepare.Bind
	prepare.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		d, a, e := bindPrepare(ctx, args)
		if e != nil {
			return d, a, e
		}
		execute := d.Execute
		d.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
			prior := selected
			selected = nil
			if prior != nil {
				ambiguous = true
			}
			result, err := execute(ctx, args)
			if err != nil || result.Failed || result.Blocked || result.Truncated {
				return result, err
			}
			var v graph.Result
			if json.Unmarshal([]byte(result.Text), &v) != nil || v.DataOmitted || len(v.Errors) != 0 || len(v.Effects) != 4 {
				return result, err
			}
			for i, step := range []string{"createPlan", "createApproach", "createTask", "setTaskBlock"} {
				x := v.Effects[i]
				if x.Field != "prepareTask" || x.Key != v.Effects[0].Key || x.Step != step || x.Status != "applied" || x.ID == "" {
					return result, err
				}
			}
			if v.Effects[2].ID == v.Effects[3].ID && !ambiguous {
				selected = &receipt{v.Effects[3].ID, scope(), fmt.Sprintf("%x", sha256.Sum256([]byte(result.Text)))}
			}
			return result, err
		}
		return d, a, nil
	}
	bindTask := func(def tool.Definition) tool.Definition {
		def.Params = append([]tool.Param(nil), def.Params...)
		for i := range def.Params {
			if def.Params[i].Name == "task" {
				def.Params[i].Required = false
				def.Params[i].AllowEmpty = true
			}
		}
		original := def
		bind := def.Bind
		def.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
			value, present := args["task"]
			id, _ := value.(string)
			snapshot := selected
			if !present {
				if snapshot == nil || ambiguous || snapshot.scope != scope() {
					return tool.Definition{}, nil, fmt.Errorf("no unambiguous current-run prepared task binding; supply an actual task ID")
				}
				id = snapshot.task
			} else if strings.TrimSpace(id) == "" {
				return tool.Definition{}, nil, fmt.Errorf("explicit task must be a nonblank actual ID; omit only for the current prepared task")
			}
			actual := tool.Args{}
			for k, v := range args {
				actual[k] = v
			}
			actual["task"] = id
			d := original
			d.Bind = nil
			var err error
			if bind != nil {
				d, actual, err = bind(ctx, actual)
				if err != nil {
					return d, actual, err
				}
			}
			d.Bound = true
			if !present {
				d.Params = append(append([]tool.Param(nil), d.Params...), tool.Param{Name: "_prepared_task", Type: tool.TypeString, Required: true}, tool.Param{Name: "_preparation_receipt_sha256", Type: tool.TypeString, Required: true})
				actual["_prepared_task"], actual["_preparation_receipt_sha256"] = id, snapshot.digest
			}
			execute := d.Execute
			d.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
				if !present && (selected != snapshot || ambiguous || snapshot.scope != scope()) {
					return tool.Result{}, fmt.Errorf("prepared task binding changed after call preflight; no call ran")
				}
				if present && (selected == nil || selected.task != id) {
					selected = nil
					ambiguous = true
				}
				result, e := execute(ctx, args)
				if original.Name == "task_start" && e == nil && !result.Failed && !result.Blocked {
					selected = nil
					ambiguous = false
				}
				return result, e
			}
			return d, actual, nil
		}
		return def
	}
	return prepare, bindTask(ready), bindTask(start)
}
