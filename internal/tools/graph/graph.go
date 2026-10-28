// Package graph exposes the existing bounded in-process API through the registry.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func New(a *api.API, limits config.GraphQLConfig, root, sessionRef, storeID string, catalogs ...*prompts.Catalog) (tool.Definition, error) {
	pc := prompts.Select(catalogs...)
	scoped, err := a.Scoped(root, sessionRef, true)
	if err != nil {
		return tool.Definition{}, err
	}
	root = scoped.CapturedProject()
	d := tool.Definition{Prompts: pc, Name: "graphql", Label: "graphql", Desc: pc.Text("tools.graphql.description"),
		Purpose: pc.Text("tools.graphql.purpose"),
		Params:  []tool.Param{{Name: "query", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.graphql.parameters.query")}, {Name: "operation_name", Type: tool.TypeString, Desc: pc.Text("tools.graphql.parameters.operation_name")}, {Name: "variables", Type: tool.TypeString, Desc: pc.Text("tools.graphql.parameters.variables")}}}
	d.GraphQLFields = scoped.AllowedFields()
	if d.GraphQLFields != nil {
		d.Desc = pc.Text("tools.graphql.knowledge_read_description")
		d.Purpose = pc.Text("tools.graphql.knowledge_read")
		for _, field := range d.GraphQLFields {
			if field == "appendKnowledge" {
				d.Desc = pc.Text("tools.graphql.knowledge_write_description")
				d.Purpose = pc.Text("tools.graphql.knowledge_write")
			}
		}
	}
	d.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
		query, _ := args["query"].(string)
		op, _ := args["operation_name"].(string)
		raw, _ := args["variables"].(string)
		if err := limits.Validate(); err != nil {
			return tool.Result{}, err
		}
		if len(query)+len(op)+len(raw) > limits.MaxRequestBytes {
			return tool.Result{}, fmt.Errorf("tools.graphql.max_request_bytes exceeded; %s", pc.Text("guidance.graph.request_cap"))
		}
		var vars map[string]any
		if raw != "" {
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&vars); err != nil || vars == nil {
				return tool.Result{}, fmt.Errorf("GraphQL variables must be one JSON object: %v", err)
			}
			var tail any
			if err := dec.Decode(&tail); err != io.EOF {
				return tool.Result{}, fmt.Errorf("GraphQL variables contain trailing JSON")
			}
		}
		text, err := scoped.ExecBounded(ctx, query, op, vars, limits)
		var schemaErrors gqlerror.List
		if errors.As(err, &schemaErrors) && len(schemaErrors) > 0 && len(schemaErrors[0].Path) == 0 {
			err = fmt.Errorf("%w; %s", err, pc.Format("guidance.graph.schema", schemaReference(query, op, schemaErrors)))
		}
		var receipt struct {
			Effects []api.Effect `json:"effects"`
		}
		changed := false
		if json.Unmarshal(text, &receipt) == nil {
			for _, effect := range receipt.Effects {
				changed = changed || effect.Status == "applied"
			}
		}
		return tool.Result{Text: string(text), Failed: err != nil, StateChanged: changed}, err
	}
	d.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		bound := d
		bound.Bind = nil
		bound.Bound = true
		bound.Params = append([]tool.Param(nil), d.Params...)
		actual := tool.Args{}
		for k, v := range args {
			actual[k] = v
		}
		for _, p := range []struct{ name, value string }{{"_project_root", root}, {"_author_session", sessionRef}, {"_store", storeID}} {
			bound.Params = append(bound.Params, tool.Param{Name: p.name, Type: tool.TypeString, AllowEmpty: true})
			actual[p.name] = p.value
		}
		return bound, actual, nil
	}
	return d, nil
}

// Preparation exposes typed conveniences, expanded to the original GraphQL action.
func Preparation(graph tool.Definition, s *store.Store, limits config.GraphQLConfig, command string, catalogs ...*prompts.Catalog) []tool.Definition {
	pc := prompts.Select(catalogs...)
	refuse := func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{}, fmt.Errorf("preparation requires registered validation and binding before execution")
	}
	expand := func(ctx context.Context, query string, vars any) (tool.Definition, tool.Args, error) {
		if err := limits.Validate(); err != nil {
			return tool.Definition{}, nil, err
		}
		raw, err := json.Marshal(vars)
		if err != nil {
			return tool.Definition{}, nil, err
		}
		if len(query)+len(raw) > limits.MaxRequestBytes {
			return tool.Definition{}, nil, fmt.Errorf("tools.graphql.max_request_bytes exceeded")
		}
		return graph.Bind(ctx, tool.Args{"query": query, "variables": string(raw)})
	}
	prepare := tool.Definition{Prompts: pc, Name: "prepare_task", Desc: pc.Text("tools.prepare_task.description"), Purpose: pc.Text("tools.prepare_task.purpose"), Execute: refuse}
	fields := []struct{ name, graph, desc string }{
		{"plan_title", "planTitle", pc.Text("tools.prepare_task.parameters.plan_title")}, {"plan_goal", "planGoal", pc.Text("tools.prepare_task.parameters.plan_goal")},
		{"approach_title", "approachTitle", pc.Text("tools.prepare_task.parameters.approach_title")}, {"task_title", "taskTitle", pc.Text("tools.prepare_task.parameters.task_title")},
		{"task_prompt", "taskPrompt", pc.Text("tools.prepare_task.parameters.task_prompt")}, {"current", "current", pc.Text("tools.prepare_task.parameters.current")},
		{"target", "target", pc.Text("tools.prepare_task.parameters.target")}, {"sketch", "sketch", pc.Text("tools.prepare_task.parameters.sketch")},
		{"acceptance", "acceptance", pc.Text("tools.prepare_task.parameters.acceptance")}, {"tests", "tests", pc.Text("tools.prepare_task.parameters.tests")},
		{"verify", "verify", pc.Text("tools.prepare_task.parameters.verify")},
		{"acceptance_command", "acceptanceCommand", pc.Text("tools.prepare_task.parameters.acceptance_command")}, {"check_command", "checkCommand", pc.Text("tools.prepare_task.parameters.check_command")},
	}
	for _, field := range fields {
		p := tool.Param{Name: field.name, Type: tool.TypeString, Required: true, Desc: field.desc}
		if field.name == "check_command" && command != "" {
			// Preserve explicit text empty so binding refuses it instead of substituting the default.
			p.Required, p.Default, p.AllowEmpty = false, command, true
			p.Desc = pc.Text("tools.prepare_task.parameters.configured_check_command")
		}
		prepare.Params = append(prepare.Params, p)
	}
	prepare.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		input, size := map[string]string{}, 0
		for _, field := range fields {
			value, _ := args[field.name].(string)
			size += len(value)
			if size > limits.MaxRequestBytes {
				return tool.Definition{}, nil, fmt.Errorf("tools.graphql.max_request_bytes exceeded")
			}
			input[field.graph] = value
		}
		if strings.TrimSpace(input["acceptanceCommand"]) == "" {
			return tool.Definition{}, nil, fmt.Errorf("acceptance_command must be a nonblank authored behavior command")
		}
		if strings.TrimSpace(input["checkCommand"]) == "" {
			return tool.Definition{}, nil, fmt.Errorf("check_command must be nonblank; %s", pc.Text("guidance.graph.configured_omission"))
		}
		source := "proposedCheckCommand"
		if command != "" && input["checkCommand"] != command {
			return tool.Definition{}, nil, fmt.Errorf("checkCommand must equal configured command %q", command)
		}
		if command != "" {
			source = "configuredCheckCommand"
		}
		return expand(ctx, fmt.Sprintf(`mutation($input:FreshTaskInput!){prepareTask(input:$input){plan{id state} approach{id state} task{id state checkCommand %s:checkCommand}}}`, source), map[string]any{"input": input})
	}
	ready := tool.Definition{Prompts: pc, Name: "ready_task", Desc: pc.Text("tools.ready_task.description"), Purpose: pc.Text("tools.ready_task.purpose"), Params: []tool.Param{{Name: "task", Type: tool.TypeString, Required: true}}, Execute: refuse}
	ready.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		if err := limits.Validate(); err != nil {
			return tool.Definition{}, nil, err
		}
		id, _ := args["task"].(string)
		if len(id) > limits.MaxRequestBytes {
			return tool.Definition{}, nil, fmt.Errorf("tools.graphql.max_request_bytes exceeded")
		}
		_, scope, err := graph.Bind(ctx, tool.Args{"query": "{version}"})
		if err != nil {
			return tool.Definition{}, nil, err
		}
		deadline := time.Now().Add(time.Duration(limits.TimeoutSeconds) * time.Second)
		read := func(parent context.Context) ([]any, error) {
			c, cancel := context.WithDeadline(parent, deadline)
			defer cancel()
			// Six selected strings: allow six escaped bytes per character plus envelope overhead.
			rows, err := s.QueryContext(c, `MATCH(p:Plan)-[:HAS_APPROACH]->(a:Approach)-[:HAS_TASK]->(t:Task) WHERE t.id=$id AND p.project_root=$root WITH p,a,t,(size(p.id)+size(p.state)+size(a.id)+size(a.state)+size(t.id)+size(t.state))*6+64 AS bytes RETURN CASE WHEN bytes <= $max THEN [p.id,p.state,a.id,a.state,t.id,t.state] ELSE [] END AS identity LIMIT 2`, map[string]any{"id": id, "root": scope["_project_root"], "max": limits.MaxResultBytes})
			if err != nil {
				return nil, err
			}
			if len(rows) != 1 {
				return nil, fmt.Errorf("task %q needs one actual same-project ancestry", id)
			}
			identity, _ := rows[0]["identity"].([]any)
			if len(identity) != 6 {
				return nil, fmt.Errorf("task ancestry exceeds tools.graphql.max_result_bytes")
			}
			return identity, nil
		}
		identity, err := read(ctx)
		if err != nil {
			return tool.Definition{}, nil, err
		}
		if identity[5] != "queued" || (identity[1] != "draft" && identity[1] != "active") || (identity[3] != "draft" && identity[3] != "paused" && identity[3] != "ready") {
			return tool.Definition{}, nil, fmt.Errorf("ready_task requires queued work under an open Draft/Active plan and Draft/Paused/Ready approach; %s", pc.Text("guidance.graph.ready_states"))
		}
		query := `mutation($plan:ID!,$approach:ID!){`
		vars := map[string]any{"plan": identity[0], "approach": identity[2]}
		if identity[1] == "draft" {
			query += `activate:transitionPlan(id:$plan,to:"active"){id state} `
		} else {
			query = `mutation($approach:ID!){`
			delete(vars, "plan")
		}
		query += `ready:transitionApproach(id:$approach,to:"ready"){id state}}`
		if identity[3] == "ready" {
			query = `query($plan:ID!,$approach:ID!){plan(id:$plan){id state} approach(id:$approach){id state}}`
			vars["plan"] = identity[0]
			if identity[1] == "draft" {
				query = `mutation($plan:ID!){activate:transitionPlan(id:$plan,to:"active"){id state approaches{id state}}}`
				delete(vars, "approach")
			}
		}
		bound, actual, err := expand(ctx, query, vars)
		if err != nil {
			return tool.Definition{}, nil, err
		}
		execute := bound.Execute
		bound.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
			current, err := read(ctx)
			if err != nil {
				return tool.Result{}, err
			}
			if !reflect.DeepEqual(identity, current) {
				return tool.Result{}, fmt.Errorf("task ancestry/state changed after readiness binding; %s", pc.Text("guidance.graph.changed_ready"))
			}
			c, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			return execute(c, args)
		}
		return bound, actual, nil
	}
	return []tool.Definition{prepare, ready}
}
