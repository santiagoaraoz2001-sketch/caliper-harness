package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestPATLegacyConfiguredCheckOmission(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	p, _ := plan.CreatePlan(s, "p", "g")
	a, _ := plan.CreateApproach(s, p.ID, "a")
	x, _ := plan.CreateTask(s, a.ID, "t", "do work")
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Coding.CheckCommand = "true"
	cfg.Templates.Path = assignedTemplates()
	r, _, e := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://unused.invalid", Model: "component", Window: 32768}, "test legacy brief")
	if e != nil {
		t.Fatal(e)
	}
	def, _ := r.Registry.Lookup("workflow_block")
	args := tool.Args{"task": x.ID, "current": "absent", "target": "present", "sketch": "write", "acceptance": "actual behavior", "tests": "assert behavior", "verify": "independent review", "acceptance_command": "true"}
	if _, e = def.Execute(context.Background(), args); e != nil {
		t.Fatalf("configured omission refused: %v", e)
	}
	saved, e := plan.GetTask(s, x.ID)
	if e != nil || saved.CheckCommand != "true" {
		t.Fatal(saved, e)
	}
	for _, explicit := range []string{"", "false"} {
		args["check_command"] = explicit
		if _, e = def.Execute(context.Background(), args); e == nil {
			t.Fatal("explicit empty/mismatched check accepted", explicit)
		}
		after, _ := plan.GetTask(s, x.ID)
		if after.CheckCommand != saved.CheckCommand {
			t.Fatal("refusal changed owning check")
		}
	}
}

// Scripted model/judge only. Kuzu claims, files, behavioral/project checks,
// independent reviewer invocation, durable receipts and lifecycle are real.
func TestPATMainChecksReviewsAndHandsOffTwoTasks(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	root, _ := filepath.EvalSymlinks(dir)
	s := openAssignedStore(t, dir)
	p, e := plan.CreatePlanContext(plan.WithProject(t.Context(), root), s, "two real artifacts", "create both requested artifacts")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	a, e := plan.CreateApproach(s, p.ID, "ordered changes")
	if e != nil {
		t.Fatal(e)
	}
	if err := os.WriteFile("first.txt", []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	firstBefore, err := os.Stat("first.txt")
	if err != nil {
		t.Fatal(err)
	}
	tasks := []*plan.Task{}
	for _, name := range []string{"first", "second"} {
		objective, sketch := "create "+name+".txt", "write requested file"
		if name == "first" {
			objective, sketch = "verify existing first.txt", "preserve existing file and verify current contents"
		}
		x, err := plan.CreateTask(s, a.ID, name, objective)
		if err != nil {
			t.Fatal(err)
		}
		current := "file absent"
		if name == "first" {
			current = "already implemented before claim; first.txt contains first"
		}
		x, err = plan.SetTaskBlock(s, x.ID, current, "exact "+name+" artifact", sketch, "exact contents", "read actual bytes", "inspect file and behavioral result", "true", "test \"$(cat "+name+".txt)\" = "+name)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, x)
	}
	if _, e = plan.Ready(t.Context(), s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); e != nil {
		t.Fatal(e)
	}
	mainCalls, reviews := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDefinition
		}
		if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		for _, m := range body.Messages {
			if strings.HasPrefix(m.Content, "Review ") {
				reviews++
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"PASS: exact artifact and current checks observed\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				return
			}
		}
		delta, reason := map[string]any{}, "tool_calls"
		call := func(name string, args any) {
			raw, _ := json.Marshal(args)
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprint("actual-", mainCalls), "type": "function", "function": map[string]string{"name": name, "arguments": string(raw)}}}
		}
		switch mainCalls {
		case 0, 6:
			call("plan", map[string]string{"invoke": "EXECUTE"})
		case 1, 7:
			name := []string{"first", "second"}[(mainCalls-1)/6]
			if !strings.Contains(body.Messages[0].Content, "exact "+name+" artifact") {
				t.Error("claimed task not installed at system role", mainCalls)
			}
			if name == "first" {
				call("read", map[string]string{"path": "first.txt"})
			} else {
				call("write", map[string]string{"path": name + ".txt", "content": name + "\n"})
			}
		case 2:
			call("check", map[string]string{})
		case 3:
			if !strings.Contains(body.Messages[len(body.Messages)-1].Content, "Current paired check passed") {
				t.Error("passed check omitted review handoff", body.Messages[len(body.Messages)-1].Content)
			}
			call("plan", map[string]string{"invoke": "STATUS"})
		case 4, 8:
			if mainCalls == 4 {
				last := body.Messages[len(body.Messages)-1].Content
				if !strings.Contains(last, "normal final answer") || !strings.Contains(last, "no tool call") || strings.Contains(last, "running, stopped or cancelled") || strings.Contains(last, "answer only the next") {
					t.Error("STATUS contradicts review completion", last)
				}
			}
			delta = map[string]any{"content": "implemented the current artifact; request actual verification"}
			reason = "stop"
		case 5, 9:
			call("plan", map[string]string{"invoke": "NEXT"})
		case 10:
			call("plan", map[string]string{"invoke": "NEXT_ARTICLE"})
		case 11:
			call("plan", map[string]string{"invoke": "COMPLETE"})
		default:
			delta = map[string]any{"content": "both artifacts have actual check and independent review receipts"}
			reason = "stop"
		}
		mainCalls++
		if mainCalls > 15 {
			t.Error("unexpected repeated handoff requests")
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer server.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Coding.CheckCommand = "true"
	cfg.Model.Runtime = "openai"
	cfg.Model.ToolProtocol = "structured"
	cfg.Templates.Path = assignedTemplates()
	bindDecisionFixture(t, &cfg, nil)
	r, _, e := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: server.URL, Model: "component", Window: 262144, MaxSteps: 20, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "handoff", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, a.ID)}}, "create both artifacts")
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = r.Run(t.Context(), "create both artifacts")
	if e != nil || r.Outcome.Status != outcome.Completed || mainCalls != 13 || reviews != 2 {
		t.Fatalf("main handoff failed: outcome=%+v calls=%d review=%d err=%v", r.Outcome, mainCalls, reviews, e)
	}
	for _, x := range tasks {
		current, e := plan.GetTask(s, x.ID)
		if e != nil || !current.Qualified() || current.Attempts != 1 || !current.Checked() {
			t.Fatal(current, e)
		}
	}
	firstAfter, err := os.Stat("first.txt")
	if err != nil || !firstBefore.ModTime().Equal(firstAfter.ModTime()) {
		t.Fatal("pre-claim artifact rewritten", err)
	}
	parent, e := plan.GetPlan(s, p.ID)
	if e != nil || parent.State != plan.PlanComplete {
		t.Fatal(parent, e)
	}
	approach, e := plan.GetApproach(s, a.ID)
	if e != nil || approach.State != plan.ApproachComplete {
		t.Fatal(approach, e)
	}
	for _, name := range []string{"first", "second"} {
		b, e := os.ReadFile(name + ".txt")
		if e != nil || string(b) != name+"\n" {
			t.Fatal(string(b), e)
		}
	}
}

func TestPATGuidedReadyBindsRealMainTask(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := f.scope.ID
	f.call(tool.Args{"current": "absent", "target": "present"})
	f.call(tool.Args{"acceptance": "exists", "tests": "actual assertion", "acceptance_command": "true"})
	for _, a := range []tool.Args{{"objective": "write file"}, {"execution": "write it"}, {"validation": "check exists"}, {"more": false}, {"verify": "inspect and check"}, {"invoke": "COMPLETE"}} {
		f.call(a)
	}
	def := f.definition()
	bound, args, e := def.Bind(context.Background(), tool.Args{"invoke": "EXECUTE"})
	if e != nil || bound.Name != "task_start" || args["task"] != id {
		t.Fatalf("guided ready delegates instead of binding current main task: %s %v %v", bound.Name, args, e)
	}
	if strings.Contains(def.Purpose, "bounded worker conversations") {
		t.Fatal("main task instruction still advertises delegation")
	}
	if f.scope.Kind != "approach" || f.scope.Store != f.d.Store.Identity() {
		t.Fatal(f.scope)
	}
}
