package run

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	graphTool "specific-harness/internal/tools/graph"
)

type planningFixture struct {
	t         *testing.T
	d         launch.Deps
	reg       *tool.Registry
	scope     *session.WorkflowScope
	judgments int
}

func newPlanningFixture(t *testing.T) *planningFixture {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	pc := prompts.Builtin()
	f := &planningFixture{t: t, reg: tool.NewRegistry(pc)}
	f.d = launch.Deps{Store: s, Workspace: dir, Prompts: pc, Wf: cfg.Workflows, Threshold: .7, Sub: cfg.Subagents}
	// Decision double only: persistence, state transitions and GraphQL are real.
	f.d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		f.judgments++
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}
	api := graph.NewWithReady(s, "test", filepath.Join(dir, "sessions"), func(ctx context.Context, id string) (*plan.Approach, error) { return launch.Ready(ctx, f.d, id) })
	g, e := graphTool.New(api, cfg.Tools.GraphQL, dir, "fixture", s.Identity(), pc)
	if e != nil {
		t.Fatal(e)
	}
	f.reg.Register(g)
	f.reg.Register(tool.Definition{Name: "check", Params: []tool.Param{{Name: "cmd", Default: "true"}}})
	f.reg.Register(launch.New(f.d))
	f.reg.Register(tool.Definition{Name: "task_start", Params: []tool.Param{{Name: "task", Type: tool.TypeString, Required: true}}, Execute: func(context.Context, tool.Args) (tool.Result, error) {
		return tool.Result{}, fmt.Errorf("isolated planning fixture has no owning main session")
	}})
	return f
}
func (f *planningFixture) definition() tool.Definition {
	f.t.Helper()
	d, e := planningDefinition(f.d, f.reg, f.scope)
	if e != nil {
		f.t.Fatal(e)
	}
	return d
}
func (f *planningFixture) call(args tool.Args) tool.Result {
	f.t.Helper()
	d := f.definition()
	raw, _ := json.Marshal(args)
	v, gap := tool.ValidateJSON(d, string(raw))
	if gap != "" {
		f.t.Fatal(gap, d.Schema())
	}
	bound, actual, e := d.Bind(context.Background(), v)
	if e != nil {
		f.t.Fatal(e)
	}
	result, e := bound.Execute(context.Background(), actual)
	if e != nil || result.Failed {
		f.t.Fatal(e, result.Text)
	}
	if result.Focus != nil {
		f.scope = &session.WorkflowScope{Store: result.Focus.Store, Kind: result.Focus.Kind, ID: result.Focus.ID}
	}
	// Every successful answer/navigation returns the actual next question,
	// including its template, without an extra empty plan invocation.
	var receipt map[string]json.RawMessage
	if json.Unmarshal([]byte(result.Text), &receipt) == nil {
		var next string
		if json.Unmarshal(receipt["next_question"], &next) != nil || next != f.definition().Schema() {
			f.t.Fatalf("receipt does not contain authoritative next question: %s", result.Text)
		}
	} else if !strings.Contains(result.Text, f.definition().Schema()) {
		f.t.Fatalf("navigation receipt omits next question: %s", result.Text)
	}
	return result
}
func (f *planningFixture) task() {
	f.call(tool.Args{})
	f.call(tool.Args{"title": "Repair", "outcome": "requested behavior", "rules": "preserve neighbors", "articles": "first change\nsecond change", "proof": "observed regression"})
	f.call(tool.Args{"strategy": "repair owner with regression"})
	f.call(tool.Args{"title": "first task", "objective": "change requested behavior"})
}
func TestGuidedPlanningQuestionsAndOwningReady(t *testing.T) {
	f := newPlanningFixture(t)
	if len(f.definition().Params) != 0 {
		t.Fatal("entry requires a flat brief")
	}
	f.task()
	tid := f.scope.ID
	if got := f.definition().Params; len(got) != 2 || got[0].Name != "current" {
		t.Fatal(got)
	}
	f.call(tool.Args{"current": "observed defect", "target": "correct behavior"})
	f.call(tool.Args{"acceptance": "assert correct behavior", "tests": "planned assertion; unexecuted", "acceptance_command": "test 1 -eq 1"})
	for n := 0; n < 2; n++ {
		for _, a := range []tool.Args{{"objective": "necessary purpose"}, {"execution": "change responsible implementation"}, {"validation": "distinguishing assertion"}, {"more": n == 0}} {
			if len(f.definition().Params) != 1 {
				t.Fatal("questions collapsed", f.definition().Schema())
			}
			f.call(a)
		}
	}
	f.call(tool.Args{"verify": "inspect implementation and rerun assertion independently"})
	task, e := plan.GetTask(f.d.Store, tid)
	if e != nil {
		t.Fatal(e)
	}
	var sketch planningSketch
	if json.Unmarshal([]byte(task.Sketch), &sketch) != nil || len(sketch.Steps) != 2 || !sketch.Complete || task.CheckCommand != "true" || task.Attempts != 0 {
		t.Fatal(task)
	}
	// Each question survives reconstruction from only store and actual selected scope.
	before := f.definition().Schema()
	copyScope := *f.scope
	f.scope = &copyScope
	if f.definition().Schema() != before {
		t.Fatal("resume changed question")
	}
	res := f.call(tool.Args{"invoke": "COMPLETE"})
	if !strings.Contains(res.Text, "ready") || f.judgments != 1 {
		t.Fatal(res, f.judgments)
	}
	def := f.definition()
	if def.Params[0].Enum[0] != "EXECUTE" {
		t.Fatal(def.Schema())
	}
	bound, args, e := def.Bind(context.Background(), tool.Args{"invoke": "EXECUTE"})
	if e != nil || bound.Name != "task_start" || args["task"] != tid {
		t.Fatal(bound, args, e)
	}
	// Binding a task is not fabricated execution; no owning main session is installed.
	result, e := bound.Execute(context.Background(), args)
	if e == nil || result.Workflow != nil && result.Workflow.Qualified {
		t.Fatal("unwired main claim qualified", result, e)
	}
}
func TestGuidedPlanningRefusesStaleForeignAndIncomplete(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	tid := f.scope.ID
	def := f.definition()
	bound, args, e := def.Bind(context.Background(), tool.Args{"current": "old", "target": "target"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = plan.SetTaskBlock(f.d.Store, tid, "concurrent", "target", "", "", "", "", "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = bound.Execute(context.Background(), args); e == nil || !strings.Contains(e.Error(), "changed") {
		t.Fatal("stale overwrite", e)
	}
	foreign := *f.scope
	foreign.Store = "foreign"
	if _, e = planningDefinition(f.d, f.reg, &foreign); e == nil {
		t.Fatal("foreign scope admitted")
	}
	other := f.d
	other.Workspace = t.TempDir()
	if _, e = planningDefinition(other, f.reg, f.scope); e == nil {
		t.Fatal("foreign project admitted")
	}
	task, _ := plan.GetTask(f.d.Store, tid)
	if task.Current != "concurrent" {
		t.Fatal("stale write applied")
	}
	scope, _ := launch.ScopeNode(f.d.Store, f.scope)
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: scope.Parent}
	d := f.definition()
	if d.Params[0].Name != "invoke" || len(d.Params[0].Enum) != 1 || d.Params[0].Enum[0] != "NEXT" {
		t.Fatal("incomplete task skipped", d.Schema())
	}
	if _, gap := tool.ValidateJSON(d, `{"invoke":"COMPLETE"}`); gap == "" {
		t.Fatal("premature COMPLETE advertised")
	}
	f.call(tool.Args{"invoke": "NEXT"})
	if f.scope.Kind != "task" || f.scope.ID != tid {
		t.Fatal("incomplete task was not selected")
	}
}

// Public builder control: this compiles against the preserved pre-change run.go
// and fails because ordinary requests have no guided plan entry.
func TestGuidedPlanningPublicEntry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Templates.Path = assignedTemplates()
	cfg.Recall.TopN = 0
	r, _, e := New(Deps{Cfg: cfg, Store: s, MaxSteps: 5, Model: "component", Server: "http://127.0.0.1:1"}, "ordinary coding")
	if e != nil {
		t.Fatal(e)
	}
	def, ok := r.Registry.Lookup("plan")
	if !ok || len(def.Params) != 0 {
		t.Fatal("ordinary request lacks zero-argument guided plan entry")
	}
}
func TestGuidedPlanningPartialAndCancellation(t *testing.T) {
	f := newPlanningFixture(t)
	f.call(tool.Args{})
	f.call(tool.Args{"title": "p", "outcome": "goal", "rules": "constraints", "articles": "article", "proof": "evidence"})
	pid := f.scope.ID
	def := f.definition()
	bound, args, e := def.Bind(context.Background(), tool.Args{"strategy": ""})
	if e == nil {
		t.Fatal("blank authored strategy admitted", bound, args)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bound, args, e = def.Bind(ctx, tool.Args{"strategy": "strategy"})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	if _, e = bound.Execute(ctx, args); e == nil {
		t.Fatal("cancelled planning mutated")
	}
	p, _ := plan.GetPlan(f.d.Store, pid)
	if p.State != plan.PlanDraft {
		t.Fatal(p)
	}
	// Force an actual second-field owner refusal after first activation applies.
	g, _ := f.reg.Lookup("graphql")
	original := g.Bind
	g.Bind = func(c context.Context, a tool.Args) (tool.Definition, tool.Args, error) {
		raw := a["variables"].(string)
		var v map[string]any
		json.Unmarshal([]byte(raw), &v)
		v["title"] = ""
		b, _ := json.Marshal(v)
		a["variables"] = string(b)
		return original(c, a)
	}
	replaced := tool.NewRegistry(f.d.Prompts)
	for _, n := range f.reg.Names() {
		d, _ := f.reg.Lookup(n)
		if n == "graphql" {
			d = g
		}
		replaced.Register(d)
	}
	f.reg = replaced
	def = f.definition()
	bound, args, e = def.Bind(context.Background(), tool.Args{"strategy": "model answer"})
	if e != nil {
		t.Fatal(e)
	}
	result, e := bound.Execute(context.Background(), args)
	if !result.Failed && e == nil {
		t.Fatal("partial mutation called success", result)
	}
	if result.Focus != nil || !strings.Contains(result.Text, "applied") || !strings.Contains(result.Text, pid) {
		t.Fatal("partial receipts lost", result, e)
	}
	p, _ = plan.GetPlan(f.d.Store, pid)
	as, _ := plan.PlanApproaches(f.d.Store, pid)
	if p.State != plan.PlanActive || len(as) != 0 {
		t.Fatal("owner partial effects not real", p, as)
	}
}
func TestGuidedPlanningRemediationAndLegacyComplete(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	tid := f.scope.ID
	node, _ := launch.ScopeNode(f.d.Store, f.scope)
	aid := node.Parent
	// Explicit failed-owner fixture, not a claimed native or qualified execution.
	if _, e := plan.SetTaskBlock(f.d.Store, tid, "c", "t", "s", "a", "tests", "review", "true", "false"); e != nil {
		t.Fatal(e)
	}
	if _, e := plan.Ready(context.Background(), f.d.Store, aid, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := plan.TransitionApproach(f.d.Store, aid, plan.ApproachRunning); e != nil {
		t.Fatal(e)
	}
	if _, e := plan.TransitionTask(f.d.Store, tid, plan.TaskRunning); e != nil {
		t.Fatal(e)
	}
	if _, e := plan.TransitionTask(f.d.Store, tid, plan.TaskFailed); e != nil {
		t.Fatal(e)
	}
	if e := plan.SetTaskReview(f.d.Store, tid, "FAIL: BEHAVIOR_MISMATCH concrete missing result"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.definition().Purpose, "BEHAVIOR_MISMATCH") {
		t.Fatal("actual review absent")
	}
	f.call(tool.Args{"invoke": "REMEDIATE", "feedback": "repair the missing result and preserve the assertion"})
	task, _ := plan.GetTask(f.d.Store, tid)
	a, _ := plan.GetApproach(f.d.Store, aid)
	if task.State != plan.TaskQueued || a.State != plan.ApproachPaused || !a.NeedsReady || !strings.Contains(task.Verify, "repair the missing result") || !strings.Contains(task.Review, "FAIL") {
		t.Fatal(task, a)
	}
	// Historical raw complete states must not surface the VALIDATED phase.
	if e := f.d.Store.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='complete'`, map[string]any{"id": aid}); e != nil {
		t.Fatal(e)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: aid}
	if strings.Contains(f.definition().Purpose, "VALIDATED:") {
		t.Fatal("legacy raw state fabricated qualification")
	}
}

func TestGuidedPlanningCompletionRequiresActualPlan(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	tid := f.scope.ID
	report, e := planningCompletion(f.d, f.scope)
	if e != nil || report.Qualified {
		t.Fatal("draft task qualified plan", report, e)
	}
	node, _ := launch.ScopeNode(f.d.Store, f.scope)
	p, _ := plan.ApproachPlan(f.d.Store, node.Parent)
	// Raw legacy state only is intentionally not qualified receipt evidence.
	if e = f.d.Store.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.state='complete'`, map[string]any{"id": p.ID}); e != nil {
		t.Fatal(e)
	}
	report, e = planningCompletion(f.d, f.scope)
	if e != nil || report.Qualified || report.Ref != p.ID {
		t.Fatal("raw parent state qualified", report, e)
	}
	task, _ := plan.GetTask(f.d.Store, tid)
	if task.Attempts != 0 {
		t.Fatal("completion probe executed task")
	}
}

// Model-authored answers and approval are doubles; forms, protected GraphQL,
// revision identities and actual stored contracts are the real owners.
func TestGuidedPlanningRevisionEmptyParent(t *testing.T) {
	f := newPlanningFixture(t)
	ctx := plan.WithProject(context.Background(), f.d.Workspace)
	p, e := plan.CreatePlanContext(ctx, f.d.Store, "p", "A")
	if e != nil {
		t.Fatal(e)
	}
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}
	digest, _, _ := plan.ContractSnapshot(ctx, f.d.Store, "plan", p.ID)
	r, e := plan.RequestRevision(ctx, f.d.Store, "plan", p.ID, digest, "user", "test", "change to B")
	if e != nil {
		t.Fatal(e)
	}
	d := f.definition()
	raw, _ := json.Marshal(tool.Args{"invoke": "EDIT", "field": "goal", "value": "B"})
	args, why := tool.ValidateJSON(d, string(raw))
	if why != "" {
		t.Fatal(why)
	}
	bound, actual, e := d.Bind(ctx, args)
	if e != nil {
		t.Fatal(e)
	}
	if bound.Name != "graphql" || bound.Label == "plan" {
		t.Fatal("revision hid its protected mutation", bound.Name, bound.Label)
	}
	result, e := bound.Execute(ctx, actual)
	if e != nil || result.Failed {
		t.Fatal(result, e)
	}
	d = f.definition()
	bound, actual, e = d.Bind(ctx, tool.Args{"invoke": "COMPLETE"})
	if e != nil {
		t.Fatal(e)
	}
	result, e = bound.Execute(ctx, actual)
	if e != nil || result.Failed || !strings.Contains(result.Text, r.ID) {
		t.Fatal("explicit resolution receipt absent", result, e)
	}
	pending, e := plan.PendingRevisions(ctx, f.d.Store, "plan", p.ID)
	if e != nil || len(pending) != 0 {
		t.Fatal(pending, e)
	}
	actualPlan, _ := plan.GetPlan(f.d.Store, p.ID)
	if actualPlan.Goal != "B" || actualPlan.State != plan.PlanDraft {
		t.Fatal("reconciliation invented execution", actualPlan)
	}
}

func TestGuidedPlanningReceiptRetainsGraphEffects(t *testing.T) {
	f := newPlanningFixture(t)
	entry := f.call(tool.Args{})
	if !strings.Contains(entry.Text, f.definition().CallTemplate()) {
		t.Fatal("entry lacks goal answer template")
	}
	res := f.call(tool.Args{"title": "Repair", "outcome": "requested behavior", "rules": "preserve neighbors", "articles": "first change", "proof": "observed regression"})
	var receipt struct {
		Data    json.RawMessage `json:"data"`
		Effects json.RawMessage `json:"effects"`
		Next    string          `json:"next_question"`
	}
	if err := json.Unmarshal([]byte(res.Text), &receipt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(receipt.Data), f.scope.ID) || len(receipt.Effects) == 0 || receipt.Next != f.definition().Schema() {
		t.Fatalf("lost created ID/effects or wrong next question: %s", res.Text)
	}
	if strings.Contains(receipt.Next, `"outcome": "<outcome>"`) || !strings.Contains(receipt.Next, `"strategy": "<strategy>"`) {
		t.Fatalf("stale goal template instead of approach: %s", receipt.Next)
	}
}
