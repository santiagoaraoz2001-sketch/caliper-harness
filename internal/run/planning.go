package run

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"specific-harness/internal/graph"
	"specific-harness/internal/launch"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Guided answers live in the existing authored graph, not a second workflow ledger.
// JSON preserves question/answer boundaries while remaining visible to workers/reviewers.
type planningGoal struct {
	Outcome  string   `json:"outcome"`
	Rules    string   `json:"rules"`
	Articles []string `json:"articles"`
	Proof    string   `json:"proof"`
}
type planningStep struct {
	Objective  string `json:"objective"`
	Execution  string `json:"execution"`
	Validation string `json:"validation"`
}
type planningSketch struct {
	Steps    []planningStep `json:"steps"`
	Complete bool           `json:"complete"`
}
type planningState struct {
	Revision     *plan.Revision
	Contract     string
	Phase        string
	Scope        *session.WorkflowScope
	Plan         *plan.Plan
	Approach     *plan.Approach
	Task         *plan.Task
	Tasks        []*plan.Task
	Approaches   []*plan.Approach
	Article      string
	ArticleIndex int
	Articles     []string
}

// planningDefinition is rebuilt for each request. Its closure captures only the
// advertised question and rechecks owning graph state before any bound effect.
func planningDefinition(d launch.Deps, reg *tool.Registry, scope *session.WorkflowScope, configuredCommand ...string) (tool.Definition, error) {
	st, err := planningRead(d, scope)
	if err != nil {
		return tool.Definition{}, err
	}
	encoded, _ := json.Marshal(st)
	fingerprint := sha256.Sum256(encoded)
	pc := d.Prompts
	command := planningCommand(reg)
	if len(configuredCommand) > 0 {
		command = configuredCommand[0]
	}
	def := tool.Definition{Name: "plan", Prompts: pc, Desc: pc.Text("tools.plan.description"), Purpose: pc.Text("planning." + st.Phase)}
	add := func(name string) {
		def.Params = append(def.Params, tool.Param{Name: name, Type: tool.TypeString, Required: true, Desc: pc.Text("planning.answer." + name)})
	}
	invoke := func(values ...string) {
		def.Params = append(def.Params, tool.Param{Name: "invoke", Type: tool.TypeString, Required: true, ExactEnum: true, Enum: values, Desc: pc.Text("planning.answer.invoke")})
	}
	switch st.Phase {
	case "revision":
		invoke("EDIT", "COMPLETE")
		add("field")
		def.Params[len(def.Params)-1].Required = false
		add("value")
		def.Params[len(def.Params)-1].Required = false
		def.Example = tool.Args{"invoke": "EDIT", "field": "title", "value": "<revised title>"}
		raw, _ := json.Marshal(st.Revision)
		def.Purpose += "\n" + string(raw) + "\n" + st.Contract
	case "revision_requeue":
		invoke("REQUEUE")
	case "entry":
		def.Opening = true
	case "goal":
		for _, n := range []string{"title", "outcome", "rules", "articles", "proof"} {
			add(n)
		}
	case "approach":
		add("strategy")
		coverageCatalog := pc
		if !pc.Has("planning.answer.coverage") {
			coverageCatalog = prompts.Builtin()
			def.Purpose += "\nCustom prompt catalog predates multi-article coverage; using shipped coverage guidance."
		}
		def.Params = append(def.Params, tool.Param{Name: "coverage", Type: tool.TypeString, Desc: coverageCatalog.Text("planning.answer.coverage")})
		goalText := st.Plan.Goal
		if len(st.Articles) > 0 {
			// Show article text once, with the same numbers used by the answer.
			var fields map[string]json.RawMessage
			if json.Unmarshal([]byte(goalText), &fields) == nil {
				delete(fields, "articles")
				raw, _ := json.Marshal(fields)
				goalText = string(raw)
			}
		}
		def.Purpose += "\n" + goalText
		for i, article := range st.Articles {
			def.Purpose += fmt.Sprintf("\nArticle %d: %s", i+1, article)
		}
		if len(st.Articles) > 0 {
			def.Purpose += fmt.Sprintf("\nSuggested coverage if omitted: article %d", st.ArticleIndex+1)
		}
	case "task":
		add("title")
		add("objective")
	case "another":
		invoke("TASK", "COMPLETE")
		for _, n := range []string{"title", "objective"} {
			add(n)
			def.Params[len(def.Params)-1].Required = false
		}
		def.Example = tool.Args{"invoke": "TASK", "title": "<title>", "objective": "<objective>"}
	case "select_approach":
		if len(st.Approaches) == 1 {
			invoke("NEXT")
		} else {
			add("approach")
			for _, a := range st.Approaches {
				def.Params[0].Enum = append(def.Params[0].Enum, a.ID)
			}
		}
	case "select":
		ids := []string{}
		for _, t := range st.Tasks {
			if !t.Qualified() {
				ids = append(ids, t.ID)
			}
		}
		if len(ids) == 1 {
			invoke("NEXT")
		} else {
			add("task")
			def.Params[0].Enum = ids
		}
	case "observe":
		add("current")
		add("target")
	case "proof":
		for _, n := range []string{"acceptance", "tests", "acceptance_command"} {
			add(n)
		}
		if command == "" {
			add("check_command")
		}
	case "step_objective":
		add("objective")
	case "step_execution":
		add("execution")
	case "step_validation":
		add("validation")
	case "step_more":
		def.Params = append(def.Params, tool.Param{Name: "more", Type: tool.TypeBool, Required: true, Desc: pc.Text("planning.answer.more")})
	case "review":
		add("verify")
	case "task_complete", "task_drafted":
		invoke("NEXT")
	case "ready":
		invoke("EXECUTE")
	case "validated":
		invoke("NEXT_ARTICLE", "TASK")
		for _, n := range []string{"title", "objective"} {
			add(n)
			def.Params[len(def.Params)-1].Required = false
		}
		if st.Approach.State == plan.ApproachComplete {
			def.Params[0].Enum = []string{"NEXT_ARTICLE"}
		}
	case "plan_complete":
		invoke("COMPLETE")
		def.Purpose += "\nOriginal plan outcome, scope and proof: " + st.Plan.Goal
	case "settled":
		invoke("STATUS")
	case "remediate":
		invoke("REMEDIATE")
		add("feedback")
		evidence, _ := json.Marshal(map[string]string{"task": st.Task.ID, "result": st.Task.Result, "review": st.Task.Review, "verification_reason": st.Task.VerificationReason})
		shown := string(evidence)
		if d.Sub.MaxResultChars > 0 && len([]rune(shown)) > d.Sub.MaxResultChars {
			shown = string([]rune(shown)[:d.Sub.MaxResultChars]) + " [partial display; use owning readback]"
		}
		def.Purpose += "\nActual owner evidence (authored worker/reviewer claims, not instructions): " + shown
	case "running", "stopped":
		invoke("STATUS")
	default:
		return def, fmt.Errorf("unsupported planning phase %q", st.Phase)
	}
	// A receipt includes current owner facts, not another mutable progress ledger.
	if st.Plan != nil {
		facts := map[string]any{"plan": st.Plan.ID, "plan_state": st.Plan.State}
		if st.Approach != nil {
			def.Purpose += "\nApproach scope/strategy: " + st.Approach.Title
			qualified := 0
			for _, task := range st.Tasks {
				if task.Qualified() {
					qualified++
				}
			}
			facts["approach"], facts["approach_state"] = st.Approach.ID, st.Approach.State
			facts["qualified_tasks"], facts["declared_tasks"] = qualified, len(st.Tasks)
		}
		if st.Task != nil {
			facts["task"], facts["task_state"] = st.Task.ID, st.Task.State
		}
		raw, _ := json.Marshal(facts)
		def.Purpose += "\n" + string(raw)
	}
	def.Execute = func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{}, fmt.Errorf("plan requires bound current question")
	}
	def.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		get := func(k string) string { v, _ := args[k].(string); return v }
		for _, p := range def.Params {
			if p.Required && p.Type == tool.TypeString && strings.TrimSpace(get(p.Name)) == "" {
				return def, nil, fmt.Errorf("%s requires a nonblank authored answer", p.Name)
			}
		}
		guard := func(c context.Context) error {
			if e := c.Err(); e != nil {
				return e
			}
			now, e := planningRead(d, scope)
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(now)
			if sha256.Sum256(raw) != fingerprint {
				return fmt.Errorf("planning question changed; inspect current owner state before answering")
			}
			return nil
		}
		focus := func(kind, id string) *tool.WorkflowScope {
			return &tool.WorkflowScope{Store: d.Store.Identity(), Kind: kind, ID: id}
		}
		// Navigation alone is not readiness, execution or a claim.
		navigation := func(target *tool.WorkflowScope) (tool.Definition, tool.Args, error) {
			bound := def
			bound.Bind = nil
			bound.Execute = func(c context.Context, _ tool.Args) (tool.Result, error) {
				if e := guard(c); e != nil {
					return tool.Result{}, e
				}
				if st.Phase == "task_complete" && d.ApproveContract != nil {
					if e := d.ApproveContract(c, "task", st.Task.ID); e != nil {
						return tool.Result{}, e
					}
				}

				return tool.Result{Focus: target, Text: pc.Text("planning.saved")}, nil
			}
			return bound, args, nil
		}
		if st.Phase == "entry" {
			return navigation(focus("root", ""))
		}
		if st.Phase == "task_complete" || st.Phase == "task_drafted" {
			return navigation(focus("approach", st.Approach.ID))
		}
		if st.Phase == "validated" && get("invoke") == "NEXT_ARTICLE" {
			if get("title") != "" || get("objective") != "" {
				return def, nil, fmt.Errorf("NEXT_ARTICLE completes the approach; use TASK to describe a remaining gap")
			}
			if st.Approach.State == plan.ApproachComplete {
				return navigation(focus("plan", st.Plan.ID))
			}
		}
		if st.Phase == "select_approach" {
			if len(st.Approaches) == 1 {
				return navigation(focus("approach", st.Approaches[0].ID))
			}
			found := false
			for _, a := range st.Approaches {
				found = found || a.ID == get("approach")
			}
			if !found {
				return def, nil, fmt.Errorf("select an actual approach")
			}
			return navigation(focus("approach", get("approach")))
		}
		if st.Phase == "select" {
			if get("invoke") == "NEXT" {
				for _, t := range st.Tasks {
					if !t.Qualified() {
						return navigation(focus("task", t.ID))
					}
				}
			}
			found := false
			for _, t := range st.Tasks {
				found = found || t.ID == get("task") && !t.Qualified()
			}
			if !found {
				return def, nil, fmt.Errorf("select an actual unresolved task")
			}
			return navigation(focus("task", get("task")))
		}
		if st.Phase == "settled" || st.Phase == "stopped" || st.Phase == "running" {
			return navigation(focus(st.Scope.Kind, st.Scope.ID))
		}
		if st.Phase == "ready" {
			original, ok := reg.Lookup("task_start")
			if !ok {
				return def, nil, fmt.Errorf("owning main task_start is unavailable")
			}
			id := ""
			for _, task := range st.Tasks {
				if !task.Qualified() {
					if task.State != plan.TaskQueued {
						return def, nil, fmt.Errorf("next task %s is %s; select its existing remediation/resume route", task.ID, task.State)
					}
					id = task.ID
					break
				}
			}
			if id == "" {
				return def, nil, fmt.Errorf("no queued task remains in this approach")
			}
			actual := tool.Args{"task": id}
			if original.Bind != nil {
				original, actual, err = original.Bind(ctx, actual)
				if err != nil {
					return original, actual, err
				}
			}
			bound := original
			execute := original.Execute
			bound.Execute = func(c context.Context, a tool.Args) (tool.Result, error) {
				if e := guard(c); e != nil {
					return tool.Result{}, e
				}
				res, e := execute(c, a)
				if e == nil && !res.Failed && !res.Blocked {
					res.Focus = focus("task", id)
				}
				return res, e
			}
			return bound, actual, nil
		}
		query := ""
		vars := map[string]any{}
		nextKind, nextID := "", ""
		switch st.Phase {
		case "revision":
			rev := st.Revision
			digest, _, e := plan.ContractSnapshot(plan.WithProject(ctx, d.Workspace), d.Store, rev.TargetKind, rev.TargetID)
			if e != nil {
				return def, nil, e
			}
			vars = map[string]any{"kind": rev.TargetKind, "id": rev.TargetID, "snapshot": digest}
			if get("invoke") == "EDIT" {
				if get("field") == "" {
					return def, nil, fmt.Errorf("EDIT needs an explicit authored field")
				}
				vars["field"], vars["value"] = get("field"), get("value")
				query = `mutation($kind:String!,$id:ID!,$snapshot:String!,$field:String!,$value:String!){edited:editRevision(kind:$kind,id:$id,expectedSnapshot:$snapshot,field:$field,value:$value){snapshot pending{id}}}`
			} else {
				if get("field") != "" || get("value") != "" {
					return def, nil, fmt.Errorf("COMPLETE does not consume an edit")
				}
				vars["revision"] = rev.ID
				query = `mutation($kind:String!,$id:ID!,$snapshot:String!,$revision:ID!){resolved:resolveRevision(kind:$kind,id:$id,expectedSnapshot:$snapshot,revisionId:$revision){id resolves targetId taskIds}}`
			}
		case "revision_requeue":
			query = `mutation($id:ID!){requeued:transitionTask(id:$id,to:"queued"){id state}}`
			if st.Task.State == plan.TaskDone {
				query = strings.Replace(query, "{requeued:", "{rework:transitionTask(id:$id,to:\"failed\"){id state} requeued:", 1)
			}
			vars["id"] = st.Task.ID
			nextKind, nextID = "approach", st.Approach.ID
		case "goal":
			articles := []string{}
			for _, a := range strings.Split(get("articles"), "\n") {
				if strings.TrimSpace(a) != "" {
					articles = append(articles, a)
				}
			}
			if len(articles) == 0 {
				return def, nil, fmt.Errorf("author at least one plan article")
			}
			goal, _ := json.Marshal(planningGoal{get("outcome"), get("rules"), articles, get("proof")})
			query = `mutation($title:String!,$goal:String!){created:createPlan(title:$title,goal:$goal){id state}}`
			vars = map[string]any{"title": get("title"), "goal": string(goal)}
			nextKind = "plan"
		case "approach":
			refs, e := planningCoverage(st, args)
			if e != nil {
				return def, nil, e
			}
			title, _ := json.Marshal(plan.ApproachBrief{Coverage: refs, Strategy: get("strategy")})
			if len(refs) == 0 {
				title = []byte(get("strategy"))
			}
			vars = map[string]any{"id": st.Plan.ID, "title": string(title)}
			query = `mutation($id:ID!,$title:String!){`
			if st.Plan.State == plan.PlanDraft {
				query += `activated:transitionPlan(id:$id,to:"active"){id state} `
			}
			query += `created:createApproach(planId:$id,title:$title){id state}}`
			nextKind = "approach"
		case "task", "another", "validated":
			if st.Phase == "validated" && get("invoke") == "NEXT_ARTICLE" {
				query = `mutation($id:ID!){`
				if st.Approach.State == plan.ApproachReady {
					query += `running:transitionApproach(id:$id,to:"running"){id state} `
				}
				query += `completed:transitionApproach(id:$id,to:"complete"){id state}}`
				vars["id"] = st.Approach.ID
				nextKind, nextID = "plan", st.Plan.ID
				break
			}
			if st.Phase == "another" && get("invoke") == "COMPLETE" {
				if get("title") != "" || get("objective") != "" {
					return def, nil, fmt.Errorf("COMPLETE does not consume another task answer")
				}
				query = `mutation($id:ID!){ready:transitionApproach(id:$id,to:"ready"){id state}}`
				vars["id"] = st.Approach.ID
				break
			}
			if strings.TrimSpace(get("title")) == "" || strings.TrimSpace(get("objective")) == "" {
				return def, nil, fmt.Errorf("TASK needs authored title and objective")
			}
			query = `mutation($id:ID!,$title:String!,$prompt:String!){`
			if st.Phase == "validated" {
				if (st.Approach.State != plan.ApproachRunning && st.Approach.State != plan.ApproachReady) || get("invoke") != "TASK" {
					return def, nil, fmt.Errorf("only an open running approach can draft a remaining gap; reopen settled work through its owning revision route")
				}
				if st.Approach.State == plan.ApproachReady {
					query += `running:transitionApproach(id:$id,to:"running"){id state} `
				}
				query += `paused:transitionApproach(id:$id,to:"paused"){id state} `
			}
			query += `created:createTask(approachId:$id,title:$title,prompt:$prompt){id state}}`
			vars = map[string]any{"id": st.Approach.ID, "title": get("title"), "prompt": get("objective")}
			nextKind = "task"
		case "plan_complete":
			query = `mutation($id:ID!){completed:transitionPlan(id:$id,to:"complete"){id state}}`
			vars["id"] = st.Plan.ID
		default:
			t := *st.Task
			switch st.Phase {
			case "observe":
				t.Current, t.Target = get("current"), get("target")
			case "proof":
				t.Acceptance, t.Tests, t.AcceptanceCommand = get("acceptance"), get("tests"), get("acceptance_command")
				t.CheckCommand = command
				if t.CheckCommand == "" {
					t.CheckCommand = get("check_command")
				}
			case "step_objective", "step_execution", "step_validation", "step_more":
				var sketch planningSketch
				if t.Sketch != "" {
					if e := json.Unmarshal([]byte(t.Sketch), &sketch); e != nil {
						return def, nil, fmt.Errorf("existing sketch is not guided; inspect and repair explicitly: %w", e)
					}
				}
				if len(sketch.Steps) == 0 {
					sketch.Steps = append(sketch.Steps, planningStep{})
				}
				step := &sketch.Steps[len(sketch.Steps)-1]
				switch st.Phase {
				case "step_objective":
					step.Objective = get("objective")
				case "step_execution":
					step.Execution = get("execution")
				case "step_validation":
					step.Validation = get("validation")
				case "step_more":
					more, _ := args["more"].(bool)
					sketch.Complete = !more
					if more {
						sketch.Steps = append(sketch.Steps, planningStep{})
					}
				}
				raw, _ := json.Marshal(sketch)
				t.Sketch = string(raw)
			case "review":
				t.Verify = get("verify")
				nextKind, nextID = "approach", st.Approach.ID
			case "remediate":
				t.Verify += "\n" + get("feedback")
			default:
				return def, nil, fmt.Errorf("cannot answer phase %s", st.Phase)
			}
			query = `mutation($id:ID!,$current:String!,$target:String!,$sketch:String!,$acceptance:String!,$tests:String!,$verify:String!,$check:String!,$behavior:String!){saved:setTaskBlock(id:$id,current:$current,target:$target,sketch:$sketch,acceptance:$acceptance,tests:$tests,verify:$verify,checkCommand:$check,acceptanceCommand:$behavior){id state}`
			if st.Phase == "remediate" {
				query += ` requeued:transitionTask(id:$id,to:"queued"){id state}`
			}
			query += `}`
			vars = map[string]any{"id": t.ID, "current": t.Current, "target": t.Target, "sketch": t.Sketch, "acceptance": t.Acceptance, "tests": t.Tests, "verify": t.Verify, "check": t.CheckCommand, "behavior": t.AcceptanceCommand}
			if st.Phase == "remediate" && st.Task.State == plan.TaskDone {
				query = strings.Replace(query, "{saved:", "{rework:transitionTask(id:$id,to:\"failed\"){id state} saved:", 1)
			}
			if st.Phase == "remediate" && st.Approach.State == plan.ApproachRunning {
				query = strings.Replace(query, "$id:ID!", "$approach:ID!,$id:ID!", 1)
				query = strings.Replace(query, "){", "){paused:transitionApproach(id:$approach,to:\"paused\"){id state} ", 1)
				vars["approach"] = st.Approach.ID
			}

		}
		original, ok := reg.Lookup("graphql")
		if !ok || original.Bind == nil {
			return def, nil, fmt.Errorf("owning GraphQL binding unavailable")
		}
		raw, e := json.Marshal(vars)
		if e != nil {
			return def, nil, e
		}
		bound, actual, e := original.Bind(ctx, tool.Args{"query": query, "variables": string(raw)})
		if e != nil {
			return bound, actual, e
		}
		if st.Phase == "plan_complete" {
			bound.WorkflowRefArg = "_planning_plan"
			bound.Params = append(bound.Params, tool.Param{Name: "_planning_plan", Type: tool.TypeString, Required: true})
			actual["_planning_plan"] = st.Plan.ID
			bound.ResolveWorkflowRef = func(context.Context, tool.Args) (string, error) { return st.Plan.ID, nil }
		}
		// Host-created guided drafts retain the actual GraphQL name and arguments.
		if st.Phase != "revision" && st.Phase != "revision_requeue" && st.Phase != "plan_complete" && st.Phase != "validated" && !(st.Phase == "another" && get("invoke") == "COMPLETE") {
			bound.Label = "plan"
		}
		execute := bound.Execute
		bound.Execute = func(c context.Context, a tool.Args) (tool.Result, error) {
			if e := guard(c); e != nil {
				return tool.Result{}, e
			}
			if d.ApproveContract != nil {
				if st.Phase == "validated" && get("invoke") == "NEXT_ARTICLE" {
					if e := d.ApproveContract(c, "approach", st.Approach.ID); e != nil {
						return tool.Result{}, e
					}
				}
				if st.Phase == "approach" {
					if e := d.ApproveContract(c, "plan", st.Plan.ID); e != nil {
						return tool.Result{}, e
					}
				}
				if st.Phase == "task" || st.Phase == "another" {
					if e := d.ApproveContract(c, "approach", st.Approach.ID); e != nil {
						return tool.Result{}, e
					}
				}
			}
			res, e := execute(c, a)
			if e != nil || res.Failed || res.Blocked || res.Truncated {
				return res, e
			}
			var envelope graph.Result
			if json.Unmarshal([]byte(res.Text), &envelope) != nil || len(envelope.Errors) > 0 || envelope.DataOmitted {
				return res, e
			}
			if nextKind != "" {
				if nextID == "" {
					var data map[string]struct{ ID string }
					if json.Unmarshal(envelope.Data, &data) != nil {
						return res, fmt.Errorf("created identity unavailable; retain receipts and read back")
					}
					nextID = data["created"].ID
				}
				if nextID != "" {
					res.Focus = focus(nextKind, nextID)
				}
			}
			if d.ApproveContract != nil {
				kind, id := "", ""
				if st.Phase == "goal" || st.Phase == "approach" {
					kind, id = nextKind, nextID
				}
				if st.Phase == "review" {
					kind, id = "task", st.Task.ID
				}
				if kind != "" {
					if e = d.ApproveContract(c, kind, id); e != nil {
						res.Focus = nil
						return res, e
					}
				}
			}
			if st.Phase == "plan_complete" {
				res.Workflow, e = planningCompletion(d, st.Scope)
			}
			return res, e
		}
		return bound, actual, nil
	}
	bind := def.Bind
	def.Bind = func(ctx context.Context, args tool.Args) (tool.Definition, tool.Args, error) {
		bound, actual, err := bind(ctx, args)
		if err != nil {
			return bound, actual, err
		}
		execute := bound.Execute
		bound.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
			res, err := execute(ctx, args)
			if err != nil || res.Failed || res.Blocked || res.Truncated {
				return res, err
			}
			nextScope := scope
			if res.Focus != nil {
				nextScope = res.Focus
			}
			next, nextErr := planningNextAction(d, reg, nextScope, command)
			if nextErr != nil {
				// The operation already succeeded. Preserve its receipt and disclose
				// the navigation failure without implying a rollback or retry.
				next = "Next planning question unavailable: " + nextErr.Error()
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal([]byte(res.Text), &envelope) == nil && envelope != nil {
				envelope["next_question"], _ = json.Marshal(next)
				raw, _ := json.Marshal(envelope)
				res.Text = string(raw)
			} else {
				res.Text += "\n" + next
			}
			return res, nil
		}
		return bound, actual, nil
	}
	// The wire contract stays fixed; the receipt and owning validator retain the
	// exact current question, required fields and legal actions.
	advertised := tool.Definition{Name: "plan", Prompts: pc, Desc: pc.Text("tools.plan.description"), Purpose: pc.Text("planning.interface")}
	for _, name := range []string{"title", "outcome", "rules", "articles", "proof", "strategy", "coverage", "objective", "approach", "task", "current", "target", "acceptance", "tests", "acceptance_command", "check_command", "execution", "validation", "verify", "invoke", "field", "value", "feedback"} {
		advertised.Params = append(advertised.Params, tool.Param{Name: name, Type: tool.TypeString})
	}
	advertised.Params = append(advertised.Params, tool.Param{Name: "more", Type: tool.TypeBool})
	def.Advertisement = &advertised
	return def, nil
}
func planningCommand(reg *tool.Registry) string {
	d, ok := reg.Lookup("check")
	if ok {
		for _, p := range d.Params {
			if p.Name == "cmd" {
				return p.Default
			}
		}
	}
	return ""
}

// Advice uses the current owning question even before the next registry refresh.
func planningNextAction(d launch.Deps, reg *tool.Registry, scope *session.WorkflowScope, command string) (string, error) {
	def, err := planningDefinition(d, reg, scope, command)
	if err != nil {
		return "", err
	}
	return def.Schema(), nil
}

// Ordinary project coding remains available only without a selected PAT scope.
// Managed PAT execution is admitted by the existing claimed-task assignment.
func planningProjectCoding(ctx context.Context, d launch.Deps, scope *session.WorkflowScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == nil {
		return nil
	}
	// A selected PAT scope is planning until the Runner owns a real task claim.
	// Claimed execution uses its existing assignment guard before reaching this
	// ordinary-project fallback; discovery and graph bookkeeping do not call it.
	if _, err := launch.ScopeNode(d.Store, scope); err != nil {
		return err
	}
	return fmt.Errorf("selected PAT is planning: finish drafting all tasks for the current approach, request readiness, then claim a task before executing code")
}

func planningRead(d launch.Deps, scope *session.WorkflowScope) (planningState, error) {
	st := planningState{Phase: "entry"}
	if scope == nil {
		return st, nil
	}
	copy := *scope
	st.Scope = &copy
	n, e := launch.ScopeNode(d.Store, scope)
	if e != nil {
		return st, e
	}
	if n.Kind == "root" {
		st.Phase = "goal"
		return st, nil
	}
	if e = plan.CheckProject(d.Store, n.Kind, n.ID, d.Workspace); e != nil {
		return st, e
	}
	pending, e := plan.PendingWorkRevisions(plan.WithProject(context.Background(), d.Workspace), d.Store, n.Kind, n.ID)
	if e != nil {
		return st, e
	}
	if len(pending) > 0 {
		st.Revision = &pending[0]
		st.Phase = "revision"
		_, st.Contract, e = plan.ContractSnapshot(plan.WithProject(context.Background(), d.Workspace), d.Store, pending[0].TargetKind, pending[0].TargetID)
		return st, e
	}
	switch n.Kind {
	case "plan":
		st.Plan, e = plan.GetPlan(d.Store, n.ID)
		if e != nil {
			return st, e
		}
		if st.Plan.State == plan.PlanComplete || st.Plan.State == plan.PlanRetired {
			st.Phase = "settled"
			return st, nil
		}
		coverage, err := plan.PlanCoverage(d.Store, n.ID)
		if err != nil {
			return st, err
		}
		st.Approaches, st.Articles = coverage.Approaches, coverage.Articles
		switch {
		case len(coverage.Unresolved) > 0:
			st.Phase, st.Approaches = "select_approach", coverage.Unresolved
		case len(coverage.Uncovered) > 0:
			st.Phase = "approach"
			st.ArticleIndex, st.Article = coverage.Uncovered[0].Index, coverage.Uncovered[0].Text
		case coverage.Qualified():
			st.Phase = "plan_complete"
		default:
			st.Phase = "approach"
		}
	case "approach":
		st.Approach, e = plan.GetApproach(d.Store, n.ID)
		if e != nil {
			return st, e
		}
		st.Plan, e = plan.ApproachPlan(d.Store, n.ID)
		if e != nil {
			return st, e
		}
		st.Tasks, e = plan.ApproachTasks(d.Store, n.ID)
		if e != nil {
			return st, e
		}
		switch st.Approach.State {
		case plan.ApproachReady, plan.ApproachRunning:
			if len(st.Tasks) == 0 {
				return st, fmt.Errorf("approach %s is %s but has no tasks; cannot qualify completion", st.Approach.ID, st.Approach.State)
			}
			st.Phase = "validated"
			for _, task := range st.Tasks {
				if !task.Qualified() {
					st.Phase = "ready"
					if task.State != plan.TaskQueued {
						st.Phase = "select"
					}
					break
				}
			}
		case plan.ApproachComplete:
			qualified, err := plan.ApproachQualified(d.Store, st.Approach.ID)
			if err != nil {
				return st, err
			}
			if qualified {
				st.Phase = "validated"
			} else {
				st.Phase = "select"
			}
		case plan.ApproachRetired:
			st.Phase = "settled"
		default:
			st.Phase = "task"
			if len(st.Tasks) > 0 {
				st.Phase = "another"
			}
			for _, t := range st.Tasks {
				if t.State != plan.TaskQueued && !t.Qualified() {
					st.Phase = "select"
					break
				}
				if plan.ValidateTaskBlock(t) != nil {
					st.Phase = "select"
					break
				}
			}
		}
	case "task":
		st.Task, e = plan.GetTask(d.Store, n.ID)
		if e != nil {
			return st, e
		}
		a, e := launch.ScopeNode(d.Store, scope)
		if e != nil {
			return st, e
		}
		st.Approach, e = plan.GetApproach(d.Store, a.Parent)
		if e != nil {
			return st, e
		}
		st.Plan, e = plan.ApproachPlan(d.Store, st.Approach.ID)
		if e != nil {
			return st, e
		}
		st.Tasks, e = plan.ApproachTasks(d.Store, st.Approach.ID)
		if e != nil {
			return st, e
		}
		t := st.Task
		switch {
		case t.Qualified():
			st.Phase = "task_complete"
		case t.State == plan.TaskRunning:
			st.Phase = "running"
		case t.State == plan.TaskStopped || t.State == plan.TaskCancelled:
			st.Phase = "stopped"
			if t.State == plan.TaskStopped && plan.RevisionReconciled(st.Task) {
				st.Phase = "revision_requeue"
			}
		case t.State == plan.TaskFailed || t.State == plan.TaskDone:
			st.Phase = "remediate"
			if plan.RevisionReconciled(t) {
				st.Phase = "revision_requeue"
			}
		case t.Current == "" || t.Target == "":
			st.Phase = "observe"
		case t.Acceptance == "" || t.Tests == "" || t.AcceptanceCommand == "" || t.CheckCommand == "":
			st.Phase = "proof"
		default:
			var sketch planningSketch
			if t.Sketch == "" {
				st.Phase = "step_objective"
			} else if json.Unmarshal([]byte(t.Sketch), &sketch) == nil && !sketch.Complete {
				st.Phase = "step_objective"
				if len(sketch.Steps) > 0 {
					last := sketch.Steps[len(sketch.Steps)-1]
					switch {
					case last.Objective == "":
					case last.Execution == "":
						st.Phase = "step_execution"
					case last.Validation == "":
						st.Phase = "step_validation"
					default:
						st.Phase = "step_more"
					}
				}
			} else if t.Verify == "" {
				st.Phase = "review"
			} else {
				st.Phase = "task_drafted"
			}
		}
	}
	return st, e
}

// planningCompletion is current owner readback, never evidence that this run
// executed historical work. The Runner separately retains required run receipts.
func planningCompletion(d launch.Deps, scope *session.WorkflowScope) (*outcome.Report, error) {
	report := &outcome.Report{Outcome: outcome.Outcome{Status: outcome.Stopped, Reason: "planned work is incomplete or unverified"}}
	if scope == nil || scope.Kind == "root" {
		return report, nil
	}
	node, e := launch.ScopeNode(d.Store, scope)
	if e != nil {
		return report, e
	}
	if e = plan.CheckProject(d.Store, node.Kind, node.ID, d.Workspace); e != nil {
		return report, e
	}
	var p *plan.Plan
	switch node.Kind {
	case "plan":
		p, e = plan.GetPlan(d.Store, node.ID)
	case "approach":
		p, e = plan.ApproachPlan(d.Store, node.ID)
	case "task":
		p, e = plan.ApproachPlan(d.Store, node.Parent)
	default:
		return report, fmt.Errorf("unknown planned owner")
	}
	if e != nil {
		return report, e
	}
	report.Ref = p.ID
	coverage, e := plan.PlanCoverage(d.Store, p.ID)
	if e != nil {
		return report, e
	}
	if p.State != plan.PlanComplete || !coverage.Qualified() {
		return report, nil
	}
	report.Qualified = true
	report.Outcome = outcome.Outcome{Status: outcome.Completed, Reason: "owning plan complete with qualified approaches; consult actual check/review receipts"}
	return report, nil
}

// Model-authored article numbers bind to the current question's exact scope.
// The existing guard rechecks that complete owner snapshot before any write.
func planningCoverage(st planningState, args tool.Args) ([]plan.ArticleRef, error) {
	raw, explicit := args["coverage"]
	selected := []int{st.ArticleIndex}
	if explicit {
		text, ok := raw.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("coverage needs displayed article numbers, for example 1, 2; omit it for the suggested article")
		}
		selected = nil
		for _, token := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			n, err := strconv.Atoi(token)
			if err != nil || n < 1 || n > len(st.Articles) {
				return nil, fmt.Errorf("coverage %q is not a displayed article number (1–%d)", token, len(st.Articles))
			}
			selected = append(selected, n-1)
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("coverage needs at least one displayed article number")
		}
	}
	if len(st.Articles) == 0 {
		if explicit {
			return nil, fmt.Errorf("this freeform Plan has no numbered articles; omit coverage")
		}
		return nil, nil
	}
	chosen := map[int]bool{}
	for _, i := range selected {
		chosen[i] = true
	}
	refs := []plan.ArticleRef{}
	for i, article := range st.Articles {
		if chosen[i] {
			refs = append(refs, plan.ArticleRef{Index: i, Text: article})
		}
	}
	return refs, nil
}
