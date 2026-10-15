package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	api "specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
	graphTool "specific-harness/internal/tools/graph"
)

// Scripted model/judge, real Runner. No inference or task qualification claimed.
func finalRepairRunner(replies []string) (*Runner, *scriptModel, *recordSink) {
	m, sink := &scriptModel{replies: replies}, &recordSink{}
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Model: m, Registry: tool.NewRegistry(), Session: sink, MaxSteps: 9, LoopStall: 3, Threshold: .7,
		Assigned: &AssignedOps{Reset: func() {}, Facts: func(context.Context) (any, error) { return BookkeepingFacts{}, nil }}}
	return r, m, sink
}

func TestNormalFinalRepairUsesExistingLoopConfirmation(t *testing.T) {
	for _, mode := range []string{"repair", "coherent fallback", "strong repeated guidance", "uncertain", "judge error"} {
		t.Run(mode, func(t *testing.T) {
			r, m, sink := finalRepairRunner([]string{"claim one", "different wording", "third claim", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth"})
			loops := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				if len(q.Options) == 3 {
					loops++
					if !strings.Contains(q.Context, "3 consecutive attempts") {
						t.Fatal(q.Context)
					}
					if mode == "judge error" {
						return decision.Verdict{}, errLoopDown
					}
					if mode == "uncertain" {
						return decision.Verdict{Option: q.Options[2], Confidence: .5}, nil
					}
					if mode == "strong repeated guidance" && loops == 1 {
						return decision.Verdict{Option: q.Options[1], Confidence: .9}, nil
					}
					return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
				}
				option := enforceOptions[1]
				if mode == "coherent fallback" {
					option = coherent
				}
				return decision.Verdict{Option: option, Confidence: .9}, nil
			}
			_, _, err := r.Run(t.Context(), "actual request")
			want, wantLoops := 9, 2
			if mode == "judge error" {
				failures := 0
				for i, kind := range sink.kinds {
					if kind == "judgment-error" && strings.Contains(sink.texts[i], errLoopDown.Error()) {
						failures++
					}
				}
				if failures != wantLoops {
					t.Fatalf("actual failed progress advice not preserved: failures=%d loops=%d", failures, loops)
				}
			}
			if err != nil || r.Outcome.Status != outcome.Stopped || len(m.seen) != want || loops != wantLoops || strings.Contains(r.Outcome.Reason, "judged") {
				t.Fatalf("advice became stop before explicit budget: err=%v outcome=%+v replies=%d loops=%d", err, r.Outcome, len(m.seen), loops)
			}

			replies, advice, done := 0, 0, 0
			for i, k := range sink.kinds {
				switch k {
				case "reply":
					replies++
					if sink.texts[i] == "" {
						t.Fatal("lost original reply")
					}
				case "enforcement":
					advice++
				case "done":
					done++
				}
			}
			if replies != want || advice < want-1 || done != 0 {
				t.Fatalf("lost raw final/repair or fabricated done: %d %d %d", replies, advice, done)
			}
		})
	}
}

func TestNormalFinalRepairOwningProgressStillResets(t *testing.T) {
	for _, kind := range []string{"read", "graphql"} {
		t.Run(kind, func(t *testing.T) {
			r, m, sink := finalRepairRunner(nil)
			root, resolveErr := filepath.EvalSymlinks(t.TempDir())
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			r.Workspace = root
			s, err := store.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), root), s, "actual plan", "actual goal")
			if err != nil {
				t.Fatal(err)
			}
			r.Continuation.Workflow = &session.WorkflowScope{Store: s.Identity(), Kind: "plan", ID: p.ID}
			r.Assigned.Facts = func(context.Context) (any, error) {
				p, e := plan.GetPlan(s, p.ID)
				if e != nil {
					return nil, e
				}
				raw, e := json.Marshal(p)
				return BookkeepingFacts{Unfinished: raw}, e
			}
			call := ""
			if kind == "read" {
				if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("new actual source\n"), 0600); err != nil {
					t.Fatal(err)
				}
				w := &files.Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
				for _, d := range w.Definitions() {
					r.Registry.Register(d)
				}
				call = "```tool\ntool: read\npath: source.txt\noffset: 1\nlimit: 1\n```"
			} else {
				d, e := graphTool.New(api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL, root, "loop-progress", s.Identity())
				if e != nil {
					t.Fatal(e)
				}
				r.Registry.Register(d)
				call = fmt.Sprintf("```tool\ntool: graphql\nquery: mutation { transitionPlan(id: %q, to: \"active\") { id state } }\n```", p.ID)
			}
			r.MaxSteps = 7
			m.replies = []string{"first final", "second final", call, "after progress one", "after progress two", "after progress three", "final bounded attempt"}
			loops := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				if len(q.Options) == 3 {
					loops++
					return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
				}
				return decision.Verdict{Option: enforceOptions[1], Confidence: .9}, nil
			}
			_, _, err = r.Run(t.Context(), "actual source task")
			if err != nil || len(m.seen) != 7 || loops != 1 || r.Outcome.Status != outcome.Stopped {
				t.Fatalf("err=%v replies=%d loops=%d outcome=%+v events=%v", err, len(m.seen), loops, r.Outcome, sink.texts)
			}
			if kind == "graphql" {
				actual, e := plan.GetPlan(s, p.ID)
				if e != nil || actual.State != plan.PlanActive {
					t.Fatalf("mutation did not execute: %+v %v", actual, e)
				}
			}
		})
	}
}

func TestNormalFinalWithoutRepairDoesNotTrip(t *testing.T) {
	r, m, _ := finalRepairRunner([]string{"finished factual answer"})
	r.Assigned = nil
	r.Continuation.Workflow = nil
	r.LoopStall = 1
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		t.Fatal("completed answer incorrectly judged as loop")
		return decision.Verdict{}, nil
	}
	text, _, err := r.Run(t.Context(), "answer")
	if err != nil || text != "finished factual answer" || r.Outcome.Status != outcome.Completed || len(m.seen) != 1 {
		t.Fatalf("%q %+v %v", text, r.Outcome, err)
	}
}

// Owning callbacks are controlled here; this checks handoff accounting, not D40 qualification.
func TestNormalFinalNewQualifiedHandoffIsNotStall(t *testing.T) {
	r, m, _ := finalRepairRunner([]string{"```tool\ntool: task_start\ntask: actual\n```", "qualified handoff", "final report"})
	r.LoopStall = 1
	r.Workspace = "/workspace"
	a := &session.Assignment{Store: "/owner", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "actual", Attempt: 1}
	r.Assigned.Admit = func(context.Context) error { return nil }
	r.Assigned.Start = func(context.Context, string) (*session.Assignment, string, error) { return a, "true", nil }
	r.Assigned.Settle = func(context.Context, outcome.Outcome, string, string, string, []llm.Message, *session.CodingState) (TaskSettlement, error) {
		return TaskSettlement{TaskID: a.TaskID, Attempt: 1, Qualified: true}, nil
	}
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		if len(q.Options) == 3 {
			t.Fatal("qualified owning handoff incorrectly judged a loop", q.Context)
		}
		return decision.Verdict{Option: coherent, Confidence: .9}, nil
	}
	r.Registry.Register(r.TaskStartDefinition())
	text, _, err := r.Run(t.Context(), "actual task")
	if err != nil || text != "final report" || r.Outcome.Status != outcome.Completed || len(m.seen) != 3 {
		t.Fatalf("%q %+v generations=%d %v", text, r.Outcome, len(m.seen), err)
	}
}
