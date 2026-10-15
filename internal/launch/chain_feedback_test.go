package launch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Model/judge doubles only; owners, registry, runner, session and restoration are real.
type chainCapture struct{ requests [][]llm.Message }

func (m *chainCapture) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	m.requests = append(m.requests, msgs)
	return "Observed preparation only.", "component", llm.Usage{}, nil
}
func TestWorkflowRoleGuideScopedAndRestored(t *testing.T) {
	s, reg := feedbackFixture(t)
	reg.Register(New(Deps{Store: s}))
	dir := filepath.Join(t.TempDir(), "sessions")
	for _, scope := range []string{"full", "coding", "construction only", "launch only"} {
		t.Run(scope, func(t *testing.T) {
			view := reg
			switch scope {
			case "coding":
				view = reg.View(agent.CodingTools)
			case "construction only":
				view = reg.View([]string{"workflow_plan"})
			case "launch only":
				view = reg.View([]string{"launch"})
			}
			m := &chainCapture{}
			r := &agent.Runner{Registry: view, Model: m, MaxSteps: 2, MaxTokens: 32768, Session: traceSession{dir}, SessionID: strings.ReplaceAll(scope, " ", "-")}
			_, _, err := r.Run(context.Background(), "inspect workflow preparation")
			if err != nil {
				t.Fatal(err)
			}
			guide := 0
			for _, msg := range m.requests[0] {
				if msg.Role == "system" {
					guide += strings.Count(msg.Content, r.Prompts.Text("system.workflow.hierarchy"))
				}
			}
			want := 0
			if scope == "full" {
				want = 1
			}
			if guide != want {
				t.Errorf("scope %s guide copies%d want%d", scope, guide, want)
			}
			if scope != "full" {
				return
			}
			events, err := session.Read(dir, r.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			restored, _, err := session.Restore(events, 32768)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range restored.Entries {
				if strings.Contains(entry.Message.Content, r.Prompts.Text("system.workflow.hierarchy")) {
					t.Fatal("injected guide serialized")
				}
			}
			m2 := &chainCapture{}
			r2 := &agent.Runner{Registry: reg, Model: m2, MaxSteps: 2, MaxTokens: 32768, Continuation: restored}
			_, _, err = r2.Run(context.Background(), "explicit next user turn")
			if err != nil {
				t.Fatal(err)
			}
			guide = 0
			for _, msg := range m2.requests[0] {
				if msg.Role == "system" {
					guide += strings.Count(msg.Content, r2.Prompts.Text("system.workflow.hierarchy"))
				}
			}
			if guide != 1 {
				t.Fatalf("restored guide copies%d", guide)
			}
		})
	}
}
func TestWorkflowProspectiveFeedbackAndAdmission(t *testing.T) {
	for _, asked := range []bool{false, true} {
		t.Run(map[bool]string{false: "confident", true: "asked"}[asked], func(t *testing.T) {
			s, reg := feedbackFixture(t)
			pid := lastWord(feedbackCall(t, reg, "workflow_plan", map[string]string{"title": "p", "goal": "two tasks"}))
			transition := feedbackCall(t, reg, "workflow_transition", map[string]string{"kind": "plan", "id": pid, "to": "active"})
			if !strings.Contains(transition, "this transition ran no children") {
				t.Errorf("transition scope %q", transition)
			}
			aid := lastWord(feedbackCall(t, reg, "workflow_approach", map[string]string{"plan": pid, "title": "a"}))
			for _, title := range []string{"alpha", "beta"} {
				tid := lastWord(feedbackCall(t, reg, "workflow_task", map[string]string{"approach": aid, "title": title, "prompt": "actual child task"}))
				text := feedbackCall(t, reg, "workflow_block", map[string]string{"task": tid, "current": "absent", "target": "artifact", "sketch": "write", "acceptance": "exit code 0", "tests": "check", "verify": "independent review", "check_command": "true", "acceptance_command": "printf FIXTURE_ACCEPTANCE"})
				if !strings.Contains(text, "this call ran no children") || !strings.Contains(text, "current advertised") || !strings.Contains(text, "readiness") {
					t.Errorf("block scope %q", text)
				}
			}
			children := 0
			resolution := ""
			d := Deps{Store: s, NewModel: func() agent.Model { children++; return &chainCapture{} }, Threshold: .7, Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
				v := decision.Verdict{Option: servesGoal, Confidence: 1}
				if asked {
					v.Confidence = .5
				}
				return v, nil
			}, Ask: func(context.Context, string) bool { return true }, Log: func(_, text string) {
				j, ok := decision.ParseJudgment(text)
				if !ok {
					t.Fatal(text)
				}
				resolution = j.Resolution
			}}
			reg = tool.NewRegistry()
			for _, def := range NewBuild(d) {
				reg.Register(def)
			}
			text := feedbackCall(t, reg, "workflow_ready", map[string]string{"approach": aid})
			if !strings.Contains(text, "this admission ran no children") || !strings.Contains(text, aid) || !strings.Contains(text, "settled report") {
				t.Errorf("ready scope %q", text)
			}
			a, err := plan.GetApproach(s, aid)
			if err != nil || a.State != plan.ApproachReady || children != 0 {
				t.Fatalf("ready effects %+v children%d err%v", a, children, err)
			}
			tasks, err := plan.ApproachTasks(s, aid)
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				if task.State != plan.TaskQueued || task.Attempts != 0 {
					t.Fatalf("ready ran task %+v", task)
				}
			}
			want := "admitted"
			if asked {
				want = "asked-admitted"
			}
			if resolution != want {
				t.Errorf("admission logged %q want%q", resolution, want)
			}
			def, _ := reg.Lookup("workflow_transition")
			raw, _ := json.Marshal(map[string]string{"kind": "approach", "id": aid, "to": "active"})
			args, q := tool.ValidateJSON(def, string(raw))
			if args != nil || q == "" {
				t.Fatal("approach kind accepted")
			}
			after, _ := plan.GetApproach(s, aid)
			if after.State != a.State || children != 0 {
				t.Fatal("refused transition had effects")
			}
			purpose := New(d).Purpose
			if !strings.Contains(purpose, "settled") || !strings.Contains(purpose, "parent") {
				t.Errorf("launch execution boundary %q", purpose)
			}
		})
	}
}
