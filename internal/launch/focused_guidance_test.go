package launch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// Actual schema, validation, owning bound mutations and readiness; no child/model execution.
func TestFocusedGuidanceMatchesArgumentsAndOwningReceipts(t *testing.T) {
	s, p, a, x, _ := focusFixture(t)
	children := 0
	d := Deps{Store: s, Wf: config.Defaults().Workflows, Threshold: .7, Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}, NewModel: func() agent.Model { children++; return &chainCapture{} }}
	reg := tool.NewRegistry()
	for _, def := range NewBuild(d) {
		reg.Register(def)
	}
	reg.Register(New(d))
	reg.Register(FocusDefinition(s, config.Defaults().Tools.Bash))
	reg.Register(tool.Definition{Name: "ask", Purpose: "GLOBAL_ASK"})
	reg.Register(tool.Definition{Name: "check", Purpose: "GLOBAL_CHECK", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Required: true}}})
	viewFor := func(ref string) *tool.Registry {
		t.Helper()
		scope, e := SelectScope(s, ref)
		if e != nil {
			t.Fatal(e)
		}
		view, _, e := FocusView(reg, s, scope)
		if e != nil {
			t.Fatal(e)
		}
		return view
	}
	execute := func(view *tool.Registry, name string, values map[string]any) tool.Result {
		t.Helper()
		def, ok := view.Lookup(name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		bound, args, e := bindFocus(t, def, values)
		if e != nil {
			t.Fatal(e)
		}
		res, e := bound.Execute(context.Background(), args)
		if e != nil || res.Blocked || (res.Failed && name != "workflow_status") {
			t.Fatalf("owning %s %+v %v", name, res, e)
		}
		return res
	}
	assertPurpose := func(view *tool.Registry, name, id, slots string) {
		t.Helper()
		defs, e := view.NativeDefinitions()
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, def := range defs {
			if def.Function.Name == name {
				found = true
				text := def.Function.Description
				if !strings.Contains(text, id) || !strings.Contains(text, "visible arguments: "+slots) || strings.Contains(text, "Use kind=") || strings.Contains(text, "Pass its UUID") {
					t.Errorf("focused native %s guidance %q", name, text)
				}
			}
		}
		if !found {
			t.Fatalf("native schema lacks %s", name)
		}
	}
	planView := viewFor(p.ID)
	assertPurpose(planView, "workflow_transition", p.ID, "to")
	transition, _ := planView.Lookup("workflow_transition")
	raw, _ := json.Marshal(map[string]string{"kind": "plan", "to": "active"})
	args, repair := tool.ValidateJSON(transition, string(raw))
	if args != nil || !strings.Contains(repair, "Current arguments: to") || !strings.Contains(repair, `unsupported arguments "kind"`) {
		t.Fatalf("hidden kind no longer refused: %v %q", args, repair)
	}
	before, _ := plan.GetPlan(s, p.ID)
	if before.State != plan.PlanDraft {
		t.Fatal("invalid hidden argument changed state")
	}
	res := execute(planView, "workflow_transition", map[string]any{"to": "active"})
	if !strings.Contains(res.Text, p.ID) || !strings.Contains(res.Text, "this transition ran no children") {
		t.Fatalf("transition truth %q", res.Text)
	}
	for _, name := range []string{"ask", "check", "workflow_focus"} {
		full, _ := reg.Lookup(name)
		focused, _ := planView.Lookup(name)
		if full.Purpose != focused.Purpose {
			t.Fatalf("global %s purpose rebound %q", name, focused.Purpose)
		}
	}
	taskView := viewFor(x.ID)
	assertPurpose(taskView, "workflow_block", x.ID, "current, target, sketch, acceptance, tests, verify, acceptance_command, check_command")
	block := map[string]any{"current": "absent", "target": "artifact", "sketch": "write", "acceptance": "actual exit0", "tests": "check", "verify": "independent verification", "check_command": "true", "acceptance_command": "printf FIXTURE_ACCEPTANCE"}
	res = execute(taskView, "workflow_block", block)
	if !strings.Contains(res.Text, "task "+x.ID+" brief saved") || !strings.Contains(res.Text, "this call ran no children") || !strings.Contains(res.Text, "select parent "+a.ID) || !strings.Contains(res.Text, "current advertised schema") || strings.Contains(res.Text, "approach=") {
		t.Errorf("focused block receipt %q", res.Text)
	}
	task, e := plan.GetTask(s, x.ID)
	if e != nil || task.CheckCommand != "true" || task.State != plan.TaskQueued || task.Attempts != 0 || children != 0 {
		t.Fatalf("block actualtask %+v children%d err%v", task, children, e)
	}
	fullBlock := feedbackCall(t, reg, "workflow_block", map[string]string{"task": x.ID, "current": "absent", "target": "artifact", "sketch": "write", "acceptance": "exit0", "tests": "check", "verify": "review"})
	if !strings.Contains(fullBlock, "readiness through the current advertised") {
		t.Errorf("full block receipt changed %q", fullBlock)
	}
	approachView := viewFor(a.ID)
	assertPurpose(approachView, "workflow_ready", a.ID, "invoke")
	res = execute(approachView, "workflow_ready", map[string]any{"invoke": "run"})
	if !strings.Contains(res.Text, "approach "+a.ID+" ready; this admission ran no children") || !strings.Contains(res.Text, "current advertised START") || !strings.Contains(res.Text, "settled report") || strings.Contains(res.Text, "approach=") {
		t.Errorf("focused ready receipt %q", res.Text)
	}
	actual, e := plan.GetApproach(s, a.ID)
	if e != nil || actual.State != plan.ApproachReady || children != 0 {
		t.Fatalf("ready actual %+v children%d err%v", actual, children, e)
	}
	readyView := viewFor(a.ID)
	assertPurpose(readyView, "launch", a.ID, "invoke")
	assertPurpose(readyView, "workflow_status", a.ID, "invoke")
	status := execute(readyView, "workflow_status", map[string]any{"invoke": "run"})
	if !strings.Contains(status.Text, a.ID) || !strings.Contains(status.Text, plan.TaskQueued) || status.Workflow == nil || status.Workflow.Qualified || status.Failed || children != 0 {
		t.Fatalf("status actual %q children%d", status.Text, children)
	}
	// Scope projection does not mutate shared full definitions or original receipts.
	fullTransition, _ := reg.Lookup("workflow_transition")
	if !strings.Contains(fullTransition.Purpose, "Use kind=plan") {
		t.Fatal("full transition altered")
	}
	fullLaunch, _ := reg.Lookup("launch")
	fullTarget := false
	for _, param := range fullLaunch.Params {
		fullTarget = fullTarget || param.Name == "approach" && param.Required && param.Type == tool.TypeString
	}
	if !fullTarget {
		t.Fatal("full launch altered")
	}
	fullPlan, _ := reg.Lookup("workflow_plan")
	rootView := viewFor("root")
	rootPlan, _ := rootView.Lookup("workflow_plan")
	if !strings.Contains(rootPlan.Purpose, reg.Prompts.Text("system.workflow.creation_focus")) || fullPlan.Purpose == rootPlan.Purpose {
		t.Errorf("creation purpose has no explicit focus %q", rootPlan.Purpose)
	}
	fullReady, _ := reg.Lookup("workflow_ready")
	if strings.Contains(fullReady.Purpose, "visible arguments:") {
		t.Fatal("full ready altered")
	}
	if children != 0 {
		t.Fatal("preparation executed children")
	}
}
