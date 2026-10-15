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

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// Only the decision response is controlled; registry, admission and Kuzu writes are real.
func TestTypedPreparationPublicBinding(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthAll, 0, "true"
	coherence := 0
	bindDecisionFixture(t, &cfg, func(q decision.Request) int { coherence++; return 0 })
	approval, allow := "", true
	r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Model: "component", Window: 32768, MaxSteps: 5,
		Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "typed",
		AskYesNo: func(_ context.Context, text string) bool { approval = text; return allow }}, "repair observed source")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prepare_task", "ready_task", "graphql", "task_start"} {
		if _, ok := r.Registry.Lookup(name); !ok {
			t.Fatalf("explicit workflow registry missing %s", name)
		}
	}
	invoke := func(name string, values tool.Args) (tool.Result, error) {
		t.Helper()
		d, _ := r.Registry.Lookup(name)
		raw, _ := json.Marshal(values)
		args, repair := tool.ValidateJSON(d, string(raw))
		if repair != "" {
			t.Fatalf("visible validation: %s", repair)
		}
		d, args, e := d.Bind(context.Background(), args)
		if e != nil {
			return tool.Result{}, e
		}
		raw, _ = json.Marshal(args)
		args, repair = tool.ValidateJSON(d, string(raw))
		if repair != "" {
			t.Fatalf("bound validation: %s", repair)
		}
		if d.Name != "graphql" || !d.Bound || args["_author_session"] != "typed" || args["_store"] != s.Identity() {
			t.Fatal("lost protected GraphQL action")
		}
		return r.Exec(context.Background(), d, args)
	}
	input := tool.Args{"plan_title": "repair", "plan_goal": "correct observed behavior", "approach_title": "small source fix", "task_title": "repair source", "task_prompt": "inspect and repair source",
		"current": "observed wrong output", "target": "correct output", "sketch": "repair owner and regression", "acceptance": "true exits0", "tests": "existing owning tests", "verify": "independently inspect source and actual check", "check_command": "true", "acceptance_command": "test -d ."}
	result, err := invoke("prepare_task", input)
	if err != nil {
		t.Fatal(err)
	}
	var envelope graph.Result
	if err = json.Unmarshal([]byte(result.Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Effects) != 4 {
		t.Fatalf("four actual preparation receipts: %s", result.Text)
	}
	for _, e := range envelope.Effects {
		if e.Status != "applied" || e.ID == "" {
			t.Fatal("missing applied ID")
		}
	}
	taskID := envelope.Effects[3].ID
	task, err := plan.GetTask(s, taskID)
	if err != nil || task.State != plan.TaskQueued || task.Current != input["current"] || task.CheckCommand != "true" {
		t.Fatalf("actual authored task %+v %v", task, err)
	}
	if !strings.Contains(approval, "prepareTask") || !strings.Contains(approval, "observed wrong output") || !strings.Contains(approval, "_project_root") {
		t.Fatalf("approval did not review actual bound action: %s", approval)
	}
	result, err = invoke("ready_task", tool.Args{"task": taskID})
	if err != nil {
		t.Fatalf("actual readiness: %s %v", result.Text, err)
	}
	a, err := plan.TaskApproach(s, taskID)
	if err != nil || a.State != plan.ApproachReady || coherence != 1 {
		t.Fatalf("owning Ready %+v %v judgments=%d", a, err, coherence)
	}
	p, err := plan.ApproachPlan(s, a.ID)
	if err != nil || p.State != plan.PlanActive {
		t.Fatalf("actual Active parent %+v %v", p, err)
	}
	if r.Continuation.Assignment != nil || r.Continuation.Workflow == nil || r.Continuation.Workflow.Kind != "root" || r.Continuation.Workflow.Store != s.Identity() {
		t.Fatal("façade invented claim/focus")
	}
	if !strings.Contains(approval, a.ID) || !strings.Contains(approval, p.ID) || !strings.Contains(approval, "transitionApproach") {
		t.Fatalf("readiness approval not bound to IDs: %s", approval)
	}
	// Already Ready is an honest readback, with no repeated judgment/admission.
	if _, err = invoke("ready_task", tool.Args{"task": taskID}); err != nil || coherence != 1 {
		t.Fatalf("Ready readback %v judgments=%d", err, coherence)
	}
	prepare, _ := r.Registry.Lookup("prepare_task")
	if _, err = prepare.Execute(context.Background(), input); err == nil {
		t.Fatal("direct facade bypass executed")
	}
	input["check_command"] = "false"
	if _, err = invoke("prepare_task", input); err == nil || !strings.Contains(err.Error(), "configured") {
		t.Fatalf("configured command mismatch admitted: %v", err)
	}
	input["check_command"] = "true"
	input["acceptance"] = " \n"
	if result, err = invoke("prepare_task", input); err == nil || !strings.Contains(result.Text, "acceptance") {
		t.Fatalf("invalid whole brief admitted: %s %v", result.Text, err)
	}
	plans, err := plan.ListPlans(s, "")
	if err != nil || len(plans) != 1 {
		t.Fatalf("rejected preparation had effects: %d %v", len(plans), err)
	}
	// Freeze Draft identity, then refuse stale binding after a real owner change.
	input["acceptance"] = "true exits0"
	result, err = invoke("prepare_task", input)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	second := envelope.Effects[3].ID
	a2, _ := plan.TaskApproach(s, second)
	p2, _ := plan.ApproachPlan(s, a2.ID)
	ready, _ := r.Registry.Lookup("ready_task")
	bound, args, err := ready.Bind(context.Background(), tool.Args{"task": second})
	if err != nil || !strings.Contains(fmt.Sprint(args["query"]), "transitionPlan") {
		t.Fatalf("Draft activation not bound: %v %v", args, err)
	}
	if _, err = plan.TransitionPlan(s, p2.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Exec(context.Background(), bound, args); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale bound ancestry admitted: %v", err)
	}
	if _, err = invoke("ready_task", tool.Args{"task": second}); err != nil {
		t.Fatalf("Active reuse failed: %v", err)
	}
	if coherence != 2 {
		t.Fatalf("Active reuse skipped owning judgment: %d", coherence)
	}
	if _, _, err = ready.Bind(context.Background(), tool.Args{"task": "missing"}); err == nil {
		t.Fatal("missing task admitted")
	}
	if _, err = plan.BindProject(context.Background(), s, p.ID, t.TempDir()); err == nil {
		t.Fatal("owner allowed rebind of existing project")
	}
	foreign, err := plan.CreatePlan(s, "foreign", "other project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.BindProject(context.Background(), s, foreign.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	foreignApproach, _ := plan.CreateApproach(s, foreign.ID, "other")
	foreignTask, _ := plan.CreateTask(s, foreignApproach.ID, "other", "other")
	if _, _, err = ready.Bind(context.Background(), tool.Args{"task": foreignTask.ID}); err == nil {
		t.Fatal("foreign task admitted")
	}
	allow = false
	if result, err = invoke("prepare_task", input); err != nil || !result.Blocked {
		t.Fatalf("denial bypass: %+v %v", result, err)
	}
	allow = true
	// Mechanical Ready failure follows actual successful activation; partial effects stay visible.
	result, err = invoke("prepare_task", input)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	third := envelope.Effects[3].ID
	a3, _ := plan.TaskApproach(s, third)
	if _, err = plan.CreateTask(s, a3.ID, "incomplete sibling", "inspect source"); err != nil {
		t.Fatal(err)
	}
	result, err = invoke("ready_task", tool.Args{"task": third})
	if err == nil {
		t.Fatalf("incomplete sibling qualified: %s", result.Text)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	if len(envelope.Effects) != 2 || envelope.Effects[0].Status != "applied" || envelope.Effects[1].Status == "applied" || envelope.Effects[0].ID == "" {
		t.Fatalf("lost partial effects: %s", result.Text)
	}
	a3, _ = plan.TaskApproach(s, third)
	p3, _ := plan.ApproachPlan(s, a3.ID)
	if p3.State != plan.PlanActive || a3.State != plan.ApproachDraft {
		t.Fatalf("fabricated rollback/readiness: %+v %+v", p3, a3)
	}
	tinyCfg := cfg
	tinyCfg.Tools.GraphQL.MaxRequestBytes = 64
	tiny, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: tinyCfg, Store: s, Model: "component", Window: 32768, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "tiny"}, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	tinyPrepare, _ := tiny.Registry.Lookup("prepare_task")
	if _, _, err = tinyPrepare.Bind(context.Background(), input); err == nil {
		t.Fatal("expanded request cap bypass")
	}
	// Original owning Graph permits Ready under Draft; the facade must still activate.
	result, err = invoke("prepare_task", input)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	fourth := envelope.Effects[3].ID
	a4, _ := plan.TaskApproach(s, fourth)
	g, _ := r.Registry.Lookup("graphql")
	bound, args, err = g.Bind(context.Background(), tool.Args{"query": fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id state}}`, a4.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if result, err = r.Exec(context.Background(), bound, args); err != nil {
		t.Fatalf("owning Draft/Ready setup: %s %v", result.Text, err)
	}
	beforeJudgments := coherence
	result, err = invoke("ready_task", tool.Args{"task": fourth})
	if err != nil {
		t.Fatalf("Draft/Ready activation: %s %v", result.Text, err)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	p4, _ := plan.ApproachPlan(s, a4.ID)
	if p4.State != plan.PlanActive || len(envelope.Effects) != 1 || envelope.Effects[0].Field != "transitionPlan" || envelope.Effects[0].Status != "applied" || !strings.Contains(result.Text, a4.ID) || coherence != beforeJudgments {
		t.Fatalf("lost activation/current Ready or repeated admission: %s parent=%s judgments=%d/%d", result.Text, p4.State, coherence, beforeJudgments)
	}
	if r.Continuation.Assignment != nil || r.Continuation.Workflow == nil || r.Continuation.Workflow.Kind != "root" || r.Continuation.Workflow.Store != s.Identity() {
		t.Fatal("Draft/Ready invented claim/focus")
	}
	result, err = invoke("prepare_task", input)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result.Text), &envelope)
	fifth := envelope.Effects[3].ID
	a5, _ := plan.TaskApproach(s, fifth)
	bound, args, err = g.Bind(context.Background(), tool.Args{"query": fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id state}}`, a5.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Exec(context.Background(), bound, args); err != nil {
		t.Fatal(err)
	}
	limitedCfg := cfg
	limitedCfg.Tools.GraphQL.MaxRows = 1
	limited, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: limitedCfg, Store: s, Model: "component", Window: 32768, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "rows", AskYesNo: func(context.Context, string) bool { return true }}, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := limited.Registry.Lookup("ready_task")
	bound, args, err = d.Bind(context.Background(), tool.Args{"task": fifth})
	if err != nil {
		t.Fatal(err)
	}
	result, err = limited.Exec(context.Background(), bound, args)
	if err == nil || json.Unmarshal([]byte(result.Text), &envelope) != nil || len(envelope.Effects) != 1 || envelope.Effects[0].Status != "applied" || envelope.Effects[0].Field != "transitionPlan" {
		t.Fatalf("aggregate read overflow lost actual activation receipt: %s %v", result.Text, err)
	}
	p5, _ := plan.ApproachPlan(s, a5.ID)
	if p5.State != plan.PlanActive {
		t.Fatal("applied activation was fabricated or rolled back")
	}
}

func TestTypedPreparationEscapedLegacyAncestryCap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	cfg.Tools.GraphQL.MaxResultBytes = 1200
	bindDecisionFixture(t, &cfg, nil)
	r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Model: "component", Window: 32768, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "escaped"}, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	g, _ := r.Registry.Lookup("graphql")
	_, protected, err := g.Bind(context.Background(), tool.Args{"query": "{version}"})
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Repeat("\x01", 200)
	if err = s.Write(`CREATE(p:Plan{id:$id,title:'legacy',goal:'goal',state:'draft',project_root:$root,created_at:TIMESTAMP('2026-01-01 00:00:00')})`, map[string]any{"id": legacy, "root": protected["_project_root"]}); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, legacy, "draft")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "inspect", "inspect source")
	if err != nil {
		t.Fatal(err)
	}
	ready, _ := r.Registry.Lookup("ready_task")
	if _, _, err = ready.Bind(context.Background(), tool.Args{"task": task.ID}); err == nil || !strings.Contains(err.Error(), "max_result_bytes") {
		t.Fatalf("escaped selected identity fetched past cap: %v", err)
	}
}

// Real registry/Graph/Kuzu/session machinery; model and decision replies are doubles.
func TestTypedPreparationNativeAndTextCorrelation(t *testing.T) {
	for _, variant := range []string{"structured", "text", "structured-omitted", "text-omitted"} {
		t.Run(variant, func(t *testing.T) {
			protocol := strings.TrimSuffix(variant, "-omitted")
			omit := strings.HasSuffix(variant, "-omitted")
			dir := t.TempDir()
			t.Chdir(dir)
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			cfg := config.Defaults()
			cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", protocol
			cfg.Coding.CheckCommand = "true"
			cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				if len(q.Options) == 5 {
					return 1
				}
				return 0
			})
			n, taskID := 0, ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if e := json.NewDecoder(request.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				wire, _ := json.Marshal(body.Messages)
				if n == 0 && protocol == "structured" {
					found := false
					for _, d := range body.Tools {
						if d.Function.Name != "prepare_task" {
							continue
						}
						props, _ := d.Function.Parameters["properties"].(map[string]any)
						p, _ := props["check_command"].(map[string]any)
						found = p["default"] == cfg.Coding.CheckCommand
						for _, required := range d.Function.Parameters["required"].([]any) {
							if required == "check_command" {
								t.Error("configured check still required on actual wire")
							}
						}
					}
					if !found {
						t.Error("actual schema lacks exact configured default")
					}
				}
				if n == 1 {
					// Actual applied preparation receipt, not an invented ID.
					for _, m := range body.Messages {
						if at := strings.Index(m.Content, `{"data":`); at >= 0 {
							var result graph.Result
							if json.Unmarshal([]byte(m.Content[at:]), &result) == nil && len(result.Effects) == 4 {
								taskID = result.Effects[3].ID
								var data struct {
									PrepareTask struct {
										Task struct{ CheckCommand, ConfiguredCheckCommand string }
									}
								}
								if e := json.Unmarshal(result.Data, &data); e != nil || data.PrepareTask.Task.CheckCommand != cfg.Coding.CheckCommand || data.PrepareTask.Task.ConfiguredCheckCommand != cfg.Coding.CheckCommand {
									t.Errorf("bound command/source absent from actual next receipt: %s %v", result.Data, e)
								}
							}
						}
					}
					if taskID == "" {
						t.Errorf("no actual preparation receipts: %s", wire)
					}
				}
				if protocol == "structured" && n > 0 {
					want := []string{"prepare_task", "ready_task", "task_start"}[n-1]
					correlated := false
					for i, m := range body.Messages {
						if m.Role == "tool" && m.Name == want && m.ToolCallID == fmt.Sprint("typed", n-1) {
							for _, previous := range body.Messages[:i] {
								for _, call := range previous.ToolCalls {
									if call.ID == m.ToolCallID && call.Name == want {
										correlated = true
									}
								}
							}
						}
					}
					if !correlated {
						t.Errorf("lost raw facade association %s: %s", want, wire)
					}
				}
				names := []string{"prepare_task", "ready_task", "task_start"}
				args := tool.Args{"task": taskID}
				if omit {
					args = tool.Args{}
				}
				if n == 0 {
					args = tool.Args{"plan_title": "repair", "plan_goal": "correct source", "approach_title": "owner fix", "task_title": "repair", "task_prompt": "repair source", "current": "observed source error", "target": "correct output", "sketch": "small fix and regression", "acceptance": "true exits0", "tests": "owning regressions", "verify": "inspect source and check", "acceptance_command": "test -d ."}
				}
				delta, finish := map[string]any{"reasoning": "component thinking-only stop"}, "stop"
				if n < len(names) {
					if protocol == "text" {
						text := "```tool\ntool: " + names[n] + "\n"
						for k, v := range args {
							text += fmt.Sprintf("%s: %s\n", k, v)
						}
						delta = map[string]any{"content": text + "```"}
					} else {
						raw, _ := json.Marshal(args)
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("typed", n), "type": "function", "function": map[string]string{"name": names[n], "arguments": string(raw)}}}}
						finish = "tool_calls"
					}
				}
				n++
				frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
			}))
			defer server.Close()
			cfg.Model.Server = server.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Server: server.URL, Model: "fixture", Window: 32768, MaxSteps: 5, Session: sink, SessionID: "wire"}, "repair source")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = r.Run(context.Background(), "repair source"); err != nil {
				t.Fatal(err)
			}
			if n != 4 || r.Continuation.Assignment == nil || r.Continuation.Assignment.TaskID != taskID || r.Continuation.Workflow == nil || r.Continuation.Workflow.Kind != "root" || r.Continuation.Workflow.Store != s.Identity() {
				t.Fatalf("explicit claim sequence requests=%d assignment=%+v focus=%+v", n, r.Continuation.Assignment, r.Continuation.Workflow)
			}
			stored, err := plan.GetTask(s, taskID)
			if err != nil || stored.CheckCommand != cfg.Coding.CheckCommand || stored.AcceptanceCommand != "test -d ." {
				t.Fatalf("actual frozen owning commands %+v %v", stored, err)
			}
			events, err := session.Read(sink.dir, "wire")
			if err != nil {
				t.Fatal(err)
			}
			boundReceipt := false
			for _, e := range events {
				if e.Kind == "bound_call" && strings.Contains(e.Text, "graphql") && strings.Contains(e.Text, "_project_root") && strings.Contains(e.Text, "prepareTask") {
					boundReceipt = true
				}
			}
			if !boundReceipt {
				t.Fatal("no durable actual protected Graph action")
			}
		})
	}
}
