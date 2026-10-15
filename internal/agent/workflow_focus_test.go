package agent_test

import (
	"context"
	"fmt"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

type focusTextModel struct {
	replies []string
	seen    [][]llm.Message
}

func (m *focusTextModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	m.seen = append(m.seen, msgs)
	if len(m.replies) == 0 {
		return "final", "fixture", llm.Usage{}, nil
	}
	v := m.replies[0]
	m.replies = m.replies[1:]
	return v, "fixture", llm.Usage{}, nil
}
func workflowRunner(t *testing.T) (*agent.Runner, *plan.Approach, *store.Store) {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	p, e := plan.CreatePlan(s, "p", "g")
	if e != nil {
		t.Fatal(e)
	}
	a, e := plan.CreateApproach(s, p.ID, "a")
	if e != nil {
		t.Fatal(e)
	}
	reg := tool.NewRegistry()
	d := launch.Deps{Store: s}
	for _, def := range launch.NewBuild(d) {
		reg.Register(def)
	}
	reg.Register(launch.New(d))
	reg.Register(launch.FocusDefinition(s, config.Defaults().Tools.Bash))
	reg.Register(tool.Definition{Name: "ask"})
	scope, e := launch.SelectScope(s, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	r := &agent.Runner{Session: &nativeRecordingSink{}, MaxSteps: 4, Continuation: session.Continuation{Workflow: scope}, WorkflowView: func(scope *session.WorkflowScope) (*tool.Registry, string, error) {
		return launch.FocusView(reg, s, scope)
	}}
	return r, a, s
}
func TestWorkflowTextExplicitInvokeAndBoundReceipt(t *testing.T) {
	r, a, _ := workflowRunner(t)
	model := &focusTextModel{replies: []string{"```tool\ntool: workflow_status\n```", "```tool\ntool: workflow_status\ninvoke: RUN\n```", "```tool\ntool: workflow_status\ninvoke: run\n```"}}
	r.Model = model
	calls := 0
	r.Exec = func(ctx context.Context, d tool.Definition, args tool.Args) (tool.Result, error) {
		calls++
		if d.Name != "workflow_status" || args["approach"] != a.ID || args["invoke"] != nil {
			t.Fatalf("original args %+v %s", args, d.Name)
		}
		return agent.Direct(ctx, d, args)
	}
	_, _, e := r.Run(context.Background(), "inspect selected approach")
	if e != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, e)
	}
	if len(model.seen) != 4 {
		t.Fatal(len(model.seen))
	}
	all := ""
	for _, m := range model.seen[1] {
		all += m.Content
	}
	if !strings.Contains(all, "Schema for workflow_status") {
		t.Fatal("bare text executed instead of schema")
	}
	receipt := ""
	for _, e := range r.Session.(*nativeRecordingSink).events {
		if e.kind == "bound_call" {
			receipt += e.text
		}
	}
	if !strings.Contains(receipt, a.ID) || strings.Contains(receipt, "invoke") {
		t.Fatalf("actual receipt %q", receipt)
	}
}
func TestWorkflowTextRecordFailureBeforeEffect(t *testing.T) {
	r, _, _ := workflowRunner(t)
	r.Session = &nativeRecordingSink{fail: "bound_call"}
	r.Model = &focusTextModel{replies: []string{"```tool\ntool: workflow_task\ntitle: forbidden\nprompt: forbidden\n```"}}
	calls := 0
	r.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		calls++
		return tool.Result{}, fmt.Errorf("must not execute")
	}
	_, _, e := r.Run(context.Background(), "record fail")
	if e == nil || calls != 0 {
		t.Fatalf("calls%d err%v", calls, e)
	}
}
func TestWorkflowNativeInvalidArrayAndFrozenScopeValue(t *testing.T) {
	r, a, s := workflowRunner(t)
	scope := r.Continuation.Workflow
	view, _, e := r.WorkflowView(scope)
	if e != nil {
		t.Fatal(e)
	}
	scope.ID = "missing" // already-created view must own its captured identity.
	def, _ := view.Lookup("workflow_task")
	args, q := tool.ValidateJSON(def, `{"title":"actual","prompt":"actual"}`)
	if q != "" {
		t.Fatal(q)
	}
	original, expanded, e := def.Bind(context.Background(), args)
	if e != nil || expanded["approach"] != a.ID {
		t.Fatalf("captured scope %v %+v", e, expanded)
	}
	if _, e = agent.Direct(context.Background(), original, expanded); e != nil {
		t.Fatal(e)
	}
	scope.ID = a.ID
	r.Model = &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "ok", Name: "workflow_task", Arguments: `{"title":"no effect","prompt":"no effect"}`}, {ID: "bad", Name: "workflow_task", Arguments: `{"title":"bad","prompt":"bad","approach":"invented"}`}}}, FinishReason: "tool_calls"}}}
	r.MaxSteps = 3
	_, _, e = r.Run(context.Background(), "invalid batch")
	if e != nil {
		t.Fatal(e)
	}
	tasks, e := plan.ApproachTasks(s, a.ID)
	if e != nil || len(tasks) != 1 || tasks[0].Title != "actual" {
		t.Fatalf("partial effect %+v %v", tasks, e)
	}
	for _, entry := range r.Continuation.Entries {
		if entry.Message.Role == "tool" && !strings.Contains(entry.Message.Content, "no calls ran") {
			t.Fatalf("refusal receipt %+v", entry)
		}
	}
}

func TestWorkflowSerialBriefReadsCurrentOwningData(t *testing.T) {
	r, a, s := workflowRunner(t)
	x, e := plan.CreateTask(s, a.ID, "x", "original")
	if e != nil {
		t.Fatal(e)
	}
	scope, e := launch.SelectScope(s, x.ID)
	if e != nil {
		t.Fatal(e)
	}
	r.Continuation.Workflow = scope
	block := `{"current":"SERIAL_FRESH_λ","target":"target","sketch":"sketch","acceptance":"proof","tests":"tests","verify":"review","check_command":"true"}`
	page := fmt.Sprintf(`{"ref":%q,"offset":0,"limit":100}`, x.ID)
	r.Model = &serialModel{replies: []llm.Reply{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "block", Name: "workflow_block", Arguments: block}, {ID: "page", Name: "workflow_focus", Arguments: page}}}, FinishReason: "tool_calls"}}}
	r.MaxSteps = 3
	_, _, e = r.Run(context.Background(), "update and read brief")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, entry := range r.Continuation.Entries {
		if entry.Message.ToolCallID == "page" {
			found = true
			if !strings.Contains(entry.Message.Content, "SERIAL_FRESH_λ") {
				t.Fatalf("stale serial page %s", entry.Message.Content)
			}
		}
	}
	if !found {
		t.Fatal("missing page association")
	}
}

func TestWorkflowPrefixReuseReplacesInjectedOnce(t *testing.T) {
	r, _, _ := workflowRunner(t)
	r.Behavior = "ONE_FIXED_BEHAVIOR"
	m := &focusTextModel{}
	r.Model = m
	for _, prompt := range []string{"ORIGINAL_INTENT", "next user request"} {
		if _, _, e := r.Run(context.Background(), prompt); e != nil {
			t.Fatal(e)
		}
	}
	base, behavior, intent := 0, 0, 0
	for _, msg := range m.seen[len(m.seen)-1] {
		if strings.HasPrefix(msg.Content, "You are a professional software engineer") {
			base++
		}
		if msg.Content == "ONE_FIXED_BEHAVIOR" {
			behavior++
		}
		if msg.Content == "ORIGINAL_INTENT" {
			intent++
		}
	}
	if base != 1 || behavior != 1 || intent != 1 {
		t.Fatalf("base%d behavior%d intent%d", base, behavior, intent)
	}
}
