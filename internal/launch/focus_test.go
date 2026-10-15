package launch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func focusFixture(t *testing.T) (*store.Store, *plan.Plan, *plan.Approach, *plan.Task, *tool.Registry) {
	t.Helper()
	s, reg := feedbackFixture(t)
	p, e := plan.CreatePlan(s, "p", "g")
	if e != nil {
		t.Fatal(e)
	}
	a, e := plan.CreateApproach(s, p.ID, "a")
	if e != nil {
		t.Fatal(e)
	}
	x, e := plan.CreateTask(s, a.ID, "x", "original prompt")
	if e != nil {
		t.Fatal(e)
	}
	reg.Register(FocusDefinition(s, config.Defaults().Tools.Bash))
	reg.Register(tool.Definition{Name: "ask"})
	return s, p, a, x, reg
}
func bindFocus(t *testing.T, d tool.Definition, args map[string]any) (tool.Definition, tool.Args, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	a, q := tool.ValidateJSON(d, string(raw))
	if q != "" {
		t.Fatal(q)
	}
	return d.Bind(context.Background(), a)
}
func TestWorkflowBriefFreshAtExecutionAndCaps(t *testing.T) {
	s, _, _, x, _ := focusFixture(t)
	cfg := config.Defaults().Tools.Bash
	d := FocusDefinition(s, cfg)

	bound, args, e := bindFocus(t, d, map[string]any{"ref": x.ID, "offset": 0, "limit": 100})
	if e != nil {
		t.Fatal(e)
	}
	_, e = plan.SetTaskBlock(s, x.ID, "FRESH_ACTUAL_λ", "target", "sketch", "proof", "tests", "review", "true")
	if e != nil {
		t.Fatal(e)
	}
	r, e := bound.Execute(context.Background(), args)
	if e != nil || !strings.Contains(r.Text, "FRESH_ACTUAL_λ") || len(r.Text) > cfg.MaxBytes {
		t.Fatalf("stale page %q %v", r.Text, e)
	}
	for _, v := range []map[string]any{{"ref": x.ID, "offset": -1}, {"ref": x.ID, "limit": 0}, {"ref": "root", "limit": 1}} {
		if _, _, e := bindFocus(t, d, v); e == nil {
			t.Fatalf("invalid page accepted %+v", v)
		}
	}
	cfg.MaxBytes = 100
	d = FocusDefinition(s, cfg)
	_, e = plan.SetTaskBlock(s, x.ID, strings.Repeat("λ", 100), "target", "sketch", "proof", "tests", "review", "true")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = bindFocus(t, d, map[string]any{"ref": x.ID, "offset": 5, "limit": 1}); e == nil || !strings.Contains(e.Error(), "no line returned") {
		t.Fatalf("long line cursor refused %v", e)
	}
	cfg.MaxBytes = 160
	d = FocusDefinition(s, cfg)
	bound, args, e = bindFocus(t, d, map[string]any{"ref": x.ID, "offset": 0, "limit": 1})
	if e != nil {
		t.Fatal(e)
	}
	r, e = bound.Execute(context.Background(), args)
	if e != nil || !strings.Contains(r.Text, "next offset=1") || len(r.Text) > 160 {
		t.Fatalf("cursor %q %v", r.Text, e)
	}
}
func TestWorkflowOwnerStateMissingForeignRetired(t *testing.T) {
	s, p, a, x, reg := focusFixture(t)
	scope, e := SelectScope(s, x.ID)
	if e != nil {
		t.Fatal(e)
	}
	view, _, e := FocusView(reg, s, scope)
	if e != nil {
		t.Fatal(e)
	}
	def, _ := view.Lookup("workflow_block")
	for _, p := range def.Params {
		if p.Name == "task" {
			t.Fatal("target exposed")
		}
	}
	if _, _, e = FocusView(tool.NewRegistry(), s, scope); e == nil {
		t.Fatal("missing owning defs silently omitted")
	}
	foreign, e := store.Open(filepath.Join(t.TempDir(), "foreign"))
	if e != nil {
		t.Fatal(e)
	}
	defer foreign.Close()
	if _, e = ScopeNode(foreign, scope); e == nil {
		t.Fatal("foreign store accepted")
	}
	missing := *scope
	missing.ID = "missing"
	if _, e = ScopeNode(s, &missing); e == nil {
		t.Fatal("missing ID accepted")
	}
	// Fixture state mutation isolates advertisement; owning transition semantics remain in plan tests.
	if e = s.Write(`MATCH (p:Plan) WHERE p.id=$id SET p.state=$state`, map[string]any{"id": p.ID, "state": plan.PlanComplete}); e != nil {
		t.Fatal(e)
	}
	if e = s.Write(`MATCH (x:Task) WHERE x.id=$id SET x.state=$state`, map[string]any{"id": x.ID, "state": plan.TaskFailed}); e != nil {
		t.Fatal(e)
	}
	view, _, e = FocusView(reg, s, scope)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := view.Lookup("workflow_transition"); ok {
		t.Fatal("closed ancestor requeue advertised")
	}
	if _, ok := view.Lookup("workflow_block"); ok {
		t.Fatal("closed ancestor edit advertised")
	}
	if e = s.Write(`MATCH (x:Task) WHERE x.id=$id SET x.state=$state`, map[string]any{"id": x.ID, "state": plan.TaskDone}); e != nil {
		t.Fatal(e)
	}
	view, _, e = FocusView(reg, s, scope)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := view.Lookup("workflow_transition"); !ok {
		t.Fatal("explicit done rework omitted")
	}
	if e = s.Write(`MATCH (a:Approach) WHERE a.id=$id SET a.state=$state`, map[string]any{"id": a.ID, "state": plan.ApproachRetired}); e != nil {
		t.Fatal(e)
	}
	if _, e = ScopeNode(s, scope); e == nil {
		t.Fatal("retired ancestor accepted")
	}
}

func TestWorkflowDirectDefinitionRequiresPreflight(t *testing.T) {
	s, _, _, x, _ := focusFixture(t)
	d := FocusDefinition(s, config.Defaults().Tools.Bash)
	if _, e := d.Execute(context.Background(), tool.Args{"ref": x.ID}); e == nil {
		t.Fatal("direct Execute did not refuse preflight")
	}
}
