package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// Only model/judge replies are doubles. Each preparation call uses the actual
// explicit full-tool Runner, registered GraphQL owner, temporary Kuzu and durable results.
func TestReceiptAdviceExplicitFullNextWire(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		if len(q.Options) == 5 {
			return 1
		}
		return 0 // actual readiness coherence component, not native qualification
	})
	n, planID, approachID, taskID := 0, "", "", ""
	var rr *agent.Runner
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Live Caliper discovery may GET any loopback listener; only inference belongs in this fixture.
		if req.Method == http.MethodGet {
			http.NotFound(w, req)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		if n > 0 {
			var envelope graph.Result
			for i := len(body.Messages) - 1; i >= 0; i-- {
				if body.Messages[i].Role == "tool" {
					text := strings.TrimPrefix(body.Messages[i].Content, "tool result:\n")
					if e := json.Unmarshal([]byte(text), &envelope); e != nil {
						t.Error(e)
					}
					break
				}
			}
			for _, effect := range envelope.Effects {
				switch effect.Field {
				case "createPlan":
					planID = effect.ID
				case "createApproach":
					approachID = effect.ID
				case "createTask":
					taskID = effect.ID
				}
			}
			wire, _ := json.Marshal(body.Messages)
			operation, id := "transitionPlan", planID
			switch n {
			case 2:
				operation = "createApproach"
			case 3:
				operation, id = "createTask", approachID
			case 4:
				operation, id = "setTaskBlock", taskID
			case 5:
				operation, id = "ready_task", taskID
			case 6:
				operation, id = "task_start", taskID
			}
			// Explicit full tools retain receipt-derived owning advice. D46 ordinary
			// execution no longer injects mandatory preparation on the next wire.
			facts, e := rr.Assigned.Facts(req.Context())
			if e != nil {
				t.Error(e)
				return
			}
			last := facts.(agent.BookkeepingFacts).Preparation
			if len(envelope.Effects) == 0 {
				t.Errorf("request%d next wire lost actual settled receipt: %+v messages=%+v", n+1, envelope, body.Messages)
			}
			if !strings.Contains(last, operation) || !strings.Contains(last, id) {
				t.Errorf("request%d expected usable %s for actual %s; guidance=%q; wirebytes=%d", n+1, operation, id, last, len(wire))
			}
		}
		queries := []string{
			`mutation { createPlan(title:"conversion",goal:"correct conversion") { id state } }`,
			fmt.Sprintf(`mutation { transitionPlan(id:%q,to:"active") { id state } }`, planID),
			fmt.Sprintf(`mutation { createApproach(planId:%q,title:"fix source and check") { id state } }`, planID),
			fmt.Sprintf(`mutation { createTask(approachId:%q,title:"conversion",prompt:"fix conversion") { id state } }`, approachID),
			fmt.Sprintf(`mutation { setTaskBlock(id:%q,current:"observed wrong formula",target:"correct conversion",sketch:"repair formula",acceptance:"command true exit0",tests:"existing conversion tests",verify:"independently inspect conversion/check",checkCommand:"true",acceptanceCommand:"test -d .") { id state } }`, taskID),
			fmt.Sprintf(`mutation { transitionApproach(id:%q,to:"ready") { id state } }`, approachID),
		}
		delta := map[string]any{"reasoning": "observed post-readiness request"}
		finish := "stop"
		if n < len(queries) {
			args, _ := json.Marshal(map[string]string{"query": queries[n]})
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("call", n), "type": "function", "function": map[string]string{"name": "graphql", "arguments": string(args)}}}}
			finish = "tool_calls"
		}
		n++
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer model.Close()
	cfg.Model.Server = model.URL
	// Six mutations plus one observed next wire; stop by the fixture budget.
	rr, _, err = New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: model.URL, Model: "component", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "receipt", Window: 262144, MaxSteps: 7}, "repair conversion")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = rr.Run(context.Background(), "repair conversion"); err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("actual requests=%d", n)
	}
	a, err := plan.GetApproach(s, approachID)
	if err != nil || a.State != plan.ApproachReady {
		t.Fatalf("actual readiness %+v %v", a, err)
	}
	if rr.Continuation.Workflow != nil || rr.Continuation.Assignment != nil {
		t.Fatal("advice changed scope/assignment")
	}
	task, err := plan.GetTask(s, taskID)
	if err != nil || task.State != plan.TaskQueued {
		t.Fatalf("advice claimed task %+v %v", task, err)
	}
}

func TestReceiptAdviceAdmissionAndRefetch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	model := codingFixture(t, "structured", nil)
	cfg.Model.Server = model.URL
	bindDecisionFixture(t, &cfg, nil)
	rr, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: model.URL, Model: "component", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "guards", Window: 32768, MaxSteps: 4}, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	advice := func() string {
		f, e := rr.Assigned.Facts(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		return f.(agent.BookkeepingFacts).Preparation
	}
	def, _ := rr.Registry.Lookup("graphql")
	// Actual public registered execution and its settled callback, as assignmentExec uses it.
	executeSettled := func(document string) (graph.Result, error) {
		raw, _ := json.Marshal(map[string]string{"query": document})
		args, why := tool.ValidateJSON(def, string(raw))
		if why != "" {
			t.Fatal(why)
		}
		bound, args, e := def.Bind(context.Background(), args)
		if e != nil {
			t.Fatal(e)
		}
		result, e := rr.Exec(context.Background(), bound, args)
		rr.Assigned.Observe(bound, args, result, e)
		var env graph.Result
		if decodeErr := json.Unmarshal([]byte(result.Text), &env); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return env, e
	}
	execute := func(document string) graph.Result {
		env, err := executeSettled(document)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	p := execute(`mutation { createPlan(title:"chosen",goal:"real goal") { id state } }`).Effects[0].ID
	if v := advice(); !strings.Contains(v, "transitionPlan") || !strings.Contains(v, p) {
		t.Fatalf("receipt not followed: %q", v)
	}
	// A second existing node is not silently chosen by a read or unrelated receipt.
	root, _ := filepath.EvalSymlinks(dir)
	ctx := plan.WithProject(context.Background(), root)
	other, e := plan.CreatePlanContext(ctx, s, "unselected", "other goal")
	if e != nil {
		t.Fatal(e)
	}
	execute(`query { plans { id state } }`)
	if v := advice(); strings.Contains(v, other.ID) || !strings.Contains(v, p) {
		t.Fatalf("read changed advisory choice %q", v)
	}
	// Same actual node is re-fetched; stale Draft advice cannot survive Active.
	if _, e = plan.TransitionPlan(s, p, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	if v := advice(); !strings.Contains(v, "createApproach") || strings.Contains(v, "transitionPlan") {
		t.Fatalf("stale state %q", v)
	}
	for _, kind := range []string{"failed", "partial", "omitted", "unknown", "unrecognized"} {
		t.Run(kind, func(t *testing.T) {
			env := graph.Result{Effects: []graph.Effect{{Field: "createPlan", Status: "applied", ID: other.ID}}}
			result := tool.Result{}
			switch kind {
			case "failed":
				result.Failed = true
			case "partial":
				env.Errors = []string{"later field failed"}
			case "omitted":
				env.DataOmitted = true
			case "unknown":
				env.Effects[0].Status = "unknown"
			case "unrecognized":
				env.Effects[0].Field = "invented"
			}
			raw, _ := json.Marshal(env)
			result.Text = string(raw)
			rr.Assigned.Observe(def, nil, result, nil)
			if v := advice(); !strings.Contains(v, p) || strings.Contains(v, other.ID) {
				t.Fatalf("invalid receipt replaced chosen advice %q", v)
			}
		})
	}
	// Real composed partial publication must not replace the last complete choice.
	partial, partialErr := executeSettled(`mutation { first:createPlan(title:"partial",goal:"preserve actual effect") { id } second:createTask(approachId:"missing",title:"bad",prompt:"bad") { id } }`)
	if partialErr == nil || len(partial.Effects) != 2 || partial.Effects[0].Status != "applied" || len(partial.Errors) == 0 {
		t.Fatalf("partial receipt lost: %+v %v", partial, partialErr)
	}
	if _, err := plan.GetPlan(s, partial.Effects[0].ID); err != nil {
		t.Fatal(err)
	}
	if v := advice(); !strings.Contains(v, p) || strings.Contains(v, partial.Effects[0].ID) {
		t.Fatalf("partial effect chose advice %q", v)
	}
	rr.Continuation.Workflow = &session.WorkflowScope{Store: s.Identity(), Kind: "plan", ID: other.ID}
	if v := advice(); !strings.Contains(v, "strategy (string, required)") || !strings.Contains(v, other.Goal) || strings.Contains(v, p) {
		t.Fatalf("advice overrode explicit scope %q", v)
	}
	facts, err := rr.Assigned.Facts(context.Background())
	if err != nil || facts.(agent.BookkeepingFacts).Checkpoint.SelectedID != other.ID {
		t.Fatalf("explicit selected owner lost: %+v %v", facts, err)
	}
	rr.Continuation.Workflow = nil
	// Closed and foreign advice targets have no preparation authority.
	// Explicit persisted closed-state fixture; not a lifecycle qualification claim.
	if e = s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.state='retired'`, map[string]any{"id": p}); e != nil {
		t.Fatal(e)
	}
	if v := advice(); !strings.Contains(v, "No matching") || strings.Contains(v, "mutation") {
		t.Fatalf("closed recipe %q", v)
	}
	foreignRoot := filepath.Join(dir, "different-project")
	foreign, e := plan.CreatePlanContext(plan.WithProject(context.Background(), foreignRoot), s, "foreign", "goal")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(graph.Result{Effects: []graph.Effect{{Field: "createPlan", Status: "applied", ID: foreign.ID}}})
	rr.Assigned.Observe(def, nil, tool.Result{Text: string(raw)}, nil)
	if v := advice(); !strings.Contains(v, "No matching") {
		t.Fatalf("foreign recipe %q", v)
	}
	// An actual incomplete sibling prevents Ready advice; completing both blocks permits it.
	a := execute(fmt.Sprintf(`mutation { createApproach(planId:%q,title:"actual strategy") { id } }`, other.ID)).Effects[0].ID
	first := execute(fmt.Sprintf(`mutation { createTask(approachId:%q,title:"first",prompt:"first work") { id } }`, a)).Effects[0].ID
	second := execute(fmt.Sprintf(`mutation { createTask(approachId:%q,title:"second",prompt:"second work") { id } }`, a)).Effects[0].ID
	block := func(id string) {
		execute(fmt.Sprintf(`mutation { setTaskBlock(id:%q,current:"observed code",target:"desired behavior",sketch:"repair",acceptance:"command true exit0",tests:"actual tests",verify:"inspect/check",checkCommand:"true",acceptanceCommand:"test -d .") { id } }`, id))
	}
	block(second)
	if v := advice(); !strings.Contains(v, "query { approach") || !strings.Contains(v, "setTaskBlock") || !strings.Contains(v, a) || strings.Contains(v, "transitionApproach") {
		t.Fatalf("incomplete sibling ignored %q", v)
	}
	block(first)
	originalRegistry := rr.Registry
	for _, parentState := range []string{plan.PlanActive, plan.PlanDraft} {
		for _, approachState := range []string{plan.ApproachDraft, plan.ApproachPaused} {
			// Actual owner rows; this controls advisory exposure, not lifecycle qualification.
			if e := s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.state=$state`, map[string]any{"id": other.ID, "state": string(parentState)}); e != nil {
				t.Fatal(e)
			}
			if e := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state=$state`, map[string]any{"id": a, "state": string(approachState)}); e != nil {
				t.Fatal(e)
			}
			if v := advice(); !strings.Contains(v, "ready_task(task=") || !strings.Contains(v, first) || !strings.Contains(v, "partial receipts") || !strings.Contains(v, "separate task_start") {
				t.Fatalf("advertised typed readiness absent (%s/%s): %q", parentState, approachState, v)
			}
			withoutReady := []string{}
			for _, name := range originalRegistry.Names() {
				if name != "ready_task" {
					withoutReady = append(withoutReady, name)
				}
			}
			rr.Registry = originalRegistry.View(withoutReady)
			if v := advice(); strings.Contains(v, "ready_task") || !strings.Contains(v, "transitionApproach") || !strings.Contains(v, a) || (parentState == plan.PlanDraft && !strings.Contains(v, "transitionPlan")) {
				t.Fatalf("current-registry fallback lost: %q", v)
			}
			rr.Registry = originalRegistry
			actual, e := plan.GetApproach(s, a)
			task, taskErr := plan.GetTask(s, first)
			if e != nil || taskErr != nil || actual.State != approachState || task.State != plan.TaskQueued || rr.Continuation.Workflow != nil || rr.Continuation.Assignment != nil {
				t.Fatalf("advice granted effects or authority: approach=%+v task=%+v errors=%v/%v", actual, task, e, taskErr)
			}
		}
	}
	if e := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='draft'`, map[string]any{"id": a}); e != nil {
		t.Fatal(e)
	}
	// Actual parent activation chooses the plan; another actual task block receipt chooses the prepared task.
	execute(fmt.Sprintf(`mutation { transitionPlan(id:%q,to:"active") { id } }`, other.ID))
	execute(fmt.Sprintf(`mutation { setTaskBlock(id:%q,current:"observed code",target:"desired behavior",sketch:"repair",acceptance:"",tests:"actual tests",verify:"inspect/check",checkCommand:"true",acceptanceCommand:"test -d .") { id } }`, second))
	refusal, refusalErr := executeSettled(fmt.Sprintf(`mutation { transitionApproach(id:%q,to:"ready") { id state } }`, a))
	owningError := plan.ValidateApproachReady(s, a)
	if refusalErr == nil || owningError == nil || len(refusal.Errors) == 0 || !strings.Contains(refusal.Errors[0], owningError.Error()) {
		t.Fatalf("actual blank acceptance must refuse readiness: %+v %v owning=%v", refusal, refusalErr, owningError)
	}
	if v := advice(); !strings.Contains(v, owningError.Error()) || !strings.Contains(v, second) || !strings.Contains(v, "setTaskBlock") || strings.Contains(v, "mutation { transitionApproach") || strings.Contains(v, "Prepared queued task: task_start") {
		t.Fatalf("owning selected-task refusal missing from advice %q", v)
	}
	block(second) // Same supported fields; complete acceptance repairs only the actual deficiency.
	if v := advice(); !strings.Contains(v, "ready_task") || !strings.Contains(v, second) || strings.Contains(v, "Prepared queued task: task_start") {
		t.Fatalf("readiness advice incorrect %q", v)
	}
	execute(fmt.Sprintf(`mutation { transitionApproach(id:%q,to:"ready") { id state } }`, a))
	actual, err := plan.GetApproach(s, a)
	if err != nil || actual.State != plan.ApproachReady {
		t.Fatalf("same factual block did not pass actual readiness: %+v %v", actual, err)
	}
	rr.Assigned.Reset()
	if v := advice(); !strings.Contains(v, "select") || strings.Contains(v, other.ID) {
		t.Fatalf("reset or no-choice invented existing node %q", v)
	}
	if rr.Continuation.Workflow != nil || rr.Continuation.Assignment != nil {
		t.Fatal("receipt advice changed authority")
	}
}

// The model and judgment are scripted doubles; GraphQL, readiness, claim,
// continuation and the next HTTP request use the real explicit full-tool public factory.
func TestPrepareTaskExplicitFullReceiptReadyClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		if len(q.Options) == 5 {
			return 1
		}
		return 0
	})
	input := map[string]string{"planTitle": "conversion", "planGoal": "correct conversion", "approachTitle": "inspected repair", "taskTitle": "conversion", "taskPrompt": "repair conversion", "current": "observed wrong formula", "target": "correct conversion", "sketch": "small formula fix", "acceptance": "command true exit0", "tests": "existing conversion tests", "verify": "independent conversion/check review", "checkCommand": "true", "acceptanceCommand": "test -d ."}
	vars, _ := json.Marshal(map[string]any{"input": input})
	n, p, a, task := 0, "", "", ""
	var rr *agent.Runner
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Live Caliper discovery may GET any loopback listener; only inference belongs in this fixture.
		if req.Method == http.MethodGet {
			http.NotFound(w, req)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		if n > 0 {
			if rr.Continuation.Workflow != nil || rr.Continuation.Assignment != nil {
				t.Error("preparation granted focus/assignment before explicit claim")
			}
			var receipt graph.Result
			for i := len(body.Messages) - 1; i >= 0; i-- {
				if body.Messages[i].Role == "tool" {
					if e := json.Unmarshal([]byte(strings.TrimPrefix(body.Messages[i].Content, "tool result:\n")), &receipt); e != nil {
						t.Error(e)
					}
					break
				}
			}
			if n == 1 {
				if len(receipt.Effects) != 4 {
					t.Errorf("lost four actual steps: %+v", receipt)
				} else {
					p, a, task = receipt.Effects[0].ID, receipt.Effects[1].ID, receipt.Effects[3].ID
				}
			}
			facts, e := rr.Assigned.Facts(req.Context())
			if e != nil {
				t.Error(e)
				return
			}
			last := facts.(agent.BookkeepingFacts).Preparation
			if len(receipt.Effects) == 0 {
				t.Error("next wire lost actual settled receipt")
			}
			if n == 1 && (p == "" || a == "" || !strings.Contains(last, task) || !strings.Contains(last, "ready_task")) {
				t.Errorf("no actual Ready recipe: %q", last)
			}
			if n == 2 && (!strings.Contains(last, task) || !strings.Contains(last, "task_start")) {
				t.Errorf("no actual claim recipe: %q", last)
			}
		}
		name, args := "graphql", map[string]any{"query": `mutation($input:FreshTaskInput!){fresh:prepareTask(input:$input){plan{id state}approach{id state}task{id state}}}`, "variables": string(vars)}
		if n == 1 {
			name, args = "ready_task", map[string]any{"task": task}
		}
		if n == 2 {
			name, args = "task_start", map[string]any{"task": task}
		}
		raw, _ := json.Marshal(args)
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("prepare", n), "type": "function", "function": map[string]string{"name": name, "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}})
		n++
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer model.Close()
	cfg.Model.Server = model.URL
	// This control tests full-tool receipts and claims, not context exhaustion.
	// The full catalog plus receipt history exceeds 32K in the frozen baseline.
	rr, _, err = New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: model.URL, Model: "component", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "prepare", Window: 65536, MaxSteps: 3}, "repair conversion")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = rr.Run(context.Background(), "repair conversion"); err != nil {
		t.Fatal(err)
	}
	actual, e := plan.GetTask(s, task)
	if n != 3 || e != nil || actual.State != plan.TaskStopped || actual.Attempts != 1 || actual.Qualified() || rr.Continuation.Workflow == nil || rr.Continuation.Workflow.Kind != "task" || rr.Continuation.Workflow.ID != task || rr.Continuation.Assignment == nil || rr.Continuation.Assignment.TaskID != task {
		t.Fatalf("false preparation/claim n=%d task=%+v continuation=%+v error=%v", n, actual, rr.Continuation, e)
	}
}
