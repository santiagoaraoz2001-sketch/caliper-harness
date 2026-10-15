package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Scripted models/owner/judge exercise the real Runner; they are not native proof.
func TestTaskRoundGuidanceRunner(t *testing.T) {
	for _, qualified := range []bool{true, false} {
		t.Run(fmt.Sprint(qualified), func(t *testing.T) {
			var effects []string
			reg := tool.NewRegistry()
			reg.Register(echoTool(&effects))
			sink := &recordSink{}
			r := &Runner{Registry: reg, Session: sink, SessionID: "round", Workspace: "/workspace", MaxSteps: 4,
				Model: &scriptModel{replies: []string{"```tool\ntool: task_start\ntask: actual-task\n```", "```tool\ntool: echo\ntext: one\n```", "```tool\ntool: echo\ntext: two\n```", "factual final"}}}
			a := &session.Assignment{Store: "/owned-store", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "actual-task", Attempt: 1}
			r.Assigned = &AssignedOps{
				Reset: func() {},
				Start: func(context.Context, string) (*session.Assignment, string, error) { return a, "true", nil },
				Admit: func(context.Context) error { return nil },
				Facts: func(context.Context) (any, error) { return map[string]string{"actual": "owner fixture"}, nil },
				Settle: func(context.Context, outcome.Outcome, string, string, string, []llm.Message, *session.CodingState) (TaskSettlement, error) {
					return TaskSettlement{TaskID: a.TaskID, Attempt: a.Attempt, Qualified: qualified}, nil
				},
			}
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				var f struct{ NormalFinal bool }
				_ = json.Unmarshal([]byte(q.Context), &f)
				option := q.Options[1]
				if f.NormalFinal {
					option = q.Options[0]
				}
				return decision.Verdict{Option: option, Confidence: 1}, nil
			}
			reg.Register(r.TaskStartDefinition())
			_, _, err := r.Run(context.Background(), "original constraint")
			if err != nil {
				t.Fatal(err)
			}
			advice := 0
			for _, e := range r.Continuation.Entries {
				if !strings.HasPrefix(e.Message.Content, "Harness bookkeeping request:") {
					continue
				}
				advice++
				want := "guidance"
				if qualified {
					want = "history"
				}
				if e.Kind != want {
					t.Errorf("qualified=%t retained advice kind=%s want=%s", qualified, e.Kind, want)
				}
				raw, _ := json.Marshal(e)
				var entry map[string]json.RawMessage
				_ = json.Unmarshal(raw, &entry)
				var round struct {
					Store, Workspace, TaskID string
					Attempt                  int
				}
				_ = json.Unmarshal(entry["round"], &round)
				if round.Store != a.Store || round.Workspace != a.Workspace || round.TaskID != a.TaskID || round.Attempt != a.Attempt {
					t.Errorf("advice not bound to actual claim: %s", raw)
				}
			}
			if advice != 1 {
				t.Errorf("exact round advice repeated %d times", advice)
			}
			rawAdvice := 0
			var events []session.Event
			for i, k := range sink.kinds {
				events = append(events, session.Event{Kind: k, Text: sink.texts[i]})
				if k == "enforcement" {
					rawAdvice++
				}
			}
			if rawAdvice < 2 {
				t.Fatal("raw repeated advice was lost")
			}
			restored, _, err := session.Restore(events, 32768)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Assignment.TaskID != a.TaskID || !strings.Contains(fmt.Sprint(restored.Messages()), "original constraint") {
				t.Fatal("restore lost provenance or intent")
			}
			if qualified {
				protected := restored.Protected(3)
				for i, e := range restored.Entries {
					if strings.HasPrefix(e.Message.Content, "Harness bookkeeping request:") && protected[i] {
						t.Fatal("retired coaching remains protected")
					}
				}
			}
			if len(effects) != 2 {
				t.Fatal(effects)
			}
		})
	}
}
