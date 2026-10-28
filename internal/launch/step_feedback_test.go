package launch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func feedbackFixture(t *testing.T) (*store.Store, *tool.Registry) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg := tool.NewRegistry()
	for _, d := range NewBuild(Deps{Store: s}) {
		reg.Register(d)
	}
	return s, reg
}
func feedbackCall(t *testing.T, reg *tool.Registry, name string, a map[string]string) string {
	t.Helper()
	d, _ := reg.Lookup(name)
	b, _ := json.Marshal(a)
	args, q := tool.ValidateJSON(d, string(b))
	if q != "" {
		t.Fatal(q)
	}
	r, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Text) >= 1024 {
		t.Fatalf("unbounded observation %d", len(r.Text))
	}
	return r.Text
}
func TestNativeWorkflowCreatedStepFeedback(t *testing.T) {
	s, reg := feedbackFixture(t)
	uuid := regexp.MustCompile(`[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}`)
	text := feedbackCall(t, reg, "workflow_plan", map[string]string{"title": "p", "goal": "two independent artifacts"})
	pid := uuid.FindString(text)
	p, err := plan.GetPlan(s, pid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "created plan "+p.ID) || !strings.Contains(text, p.State) || !strings.Contains(text, "approach parent "+p.ID) || !strings.Contains(text, "next advertised schema") || strings.Contains(text, "workflow_approach(plan=") {
		t.Errorf("plan step %q", text)
	}
	feedbackCall(t, reg, "workflow_transition", map[string]string{"kind": "plan", "id": pid, "to": "active"})
	text = feedbackCall(t, reg, "workflow_approach", map[string]string{"plan": pid, "title": "one strategy"})
	aid := uuid.FindString(text)
	a, err := plan.GetApproach(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if owner.ID != p.ID || !strings.Contains(text, "created approach "+a.ID) || !strings.Contains(text, a.State) || !strings.Contains(text, p.ID) || !strings.Contains(text, "next advertised schema") || strings.Contains(text, "workflow_task(approach=") {
		t.Errorf("approach step %q owner%+v", text, owner)
	}
	text = feedbackCall(t, reg, "workflow_task", map[string]string{"approach": aid, "title": "one task", "prompt": "write exact artifact"})
	tid := uuid.FindString(text)
	task, err := plan.GetTask(s, tid)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := plan.ApproachTasks(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != tid || !strings.Contains(text, "created task "+task.ID) || !strings.Contains(text, task.State) || !strings.Contains(text, aid) || !strings.Contains(text, "current") || !strings.Contains(text, "behavior/check/reviewer brief") || strings.Contains(text, "workflow_block(task=") {
		t.Errorf("task step %q tasks%+v", text, tasks)
	}
	block := map[string]string{"task": tid, "current": "absent", "target": "exact artifact", "sketch": "write it", "acceptance": "check exit0", "tests": "actual command", "verify": "independent reviewer", "check_command": "test -f artifact"}
	feedbackCall(t, reg, "workflow_block", block)
	delete(block, "check_command")
	block["current"] = "updated brief"
	feedbackCall(t, reg, "workflow_block", block)
	after, err := plan.GetTask(s, tid)
	if err != nil || after.CheckCommand != "test -f artifact" || after.Current != "updated brief" {
		t.Fatalf("omitted command altered %+v %v", after, err)
	}
}
func TestNativeWorkflowCaptured666RepairNoEffects(t *testing.T) {
	s, reg := feedbackFixture(t)
	p, err := plan.CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "Generate alpha.txt", "actual task")
	if err != nil {
		t.Fatal(err)
	}
	task, err = plan.GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reg.Lookup("workflow_block")
	raw := `{"current":"alpha.txt does not exist","sketch":"Write 'alpha' to alpha.txt with a newline","target":"alpha.txt contains 'alpha\\n'","task":"Generate alpha.txt","tests":"python3 checks/check_alpha.py","verify":"No review required","approach":"94c3bd61-4ef1-41da-9892-ab9f62f8f155","acceptance":"alpha.txt exists and matches 'alpha\\n'"}`
	args, q := tool.ValidateJSON(d, raw)
	if args != nil || !strings.Contains(q, `Remove unsupported arguments "approach"`) || !strings.Contains(q, "task, current, target, sketch, acceptance, tests, verify, acceptance_command, check_command") || !strings.Contains(q, "Current legal call:") || len(q) >= 1024 {
		t.Errorf("unknown repair args%v q%q", args, q)
	}
	after, _ := plan.GetTask(s, task.ID)
	if !reflect.DeepEqual(task, after) {
		t.Fatal("refusal mutated task")
	}
	args, q = tool.ValidateJSON(d, `{"current":"absent","target":"exact","sketch":"write","acceptance":"check0","tests":"test","verify":"review"}`)
	if args != nil || !strings.Contains(q, "UUID from workflow_task") {
		t.Errorf("missing task repair %q", q)
	}
	args, q = tool.ValidateJSON(d, `{"task":"Generate alpha.txt","current":"absent","target":"exact","sketch":"write","acceptance":"check0","tests":"test","verify":"review"}`)
	if q != "" {
		t.Fatal(q)
	}
	if _, err = d.Execute(context.Background(), args); err == nil {
		t.Fatal("title accepted as UUID")
	}
	after, _ = plan.GetTask(s, task.ID)
	tasks, _ := plan.ApproachTasks(s, a.ID)
	if !reflect.DeepEqual(task, after) || len(tasks) != 1 {
		t.Fatal("invalid title mutated/recreated task")
	}
}
