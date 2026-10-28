package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestSupervisionProgressSilenceAndConfidenceFloor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pick       int
		confidence float64
		nudge      bool
	}{
		{"continue confident", 0, .99, false}, {"continue uncertain", 0, .499, false},
		{"act failed confidence", 1, .499, false}, {"reconsider failed confidence", 2, .499, false},
		{"unsupported repair exact floor", 1, .5, false}, {"reconsider above floor", 2, .6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{replies: []string{echoBlock("same"), echoBlock("same"), echoBlock("same"), "final answer"}}
			judgments := 0
			r, _ := loopRunner(model, nil, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Option: q.Options[tc.pick], Confidence: tc.confidence}, nil
			})
			sink := &recordSink{}
			r.Session, r.SessionID = sink, "confidence-floor"
			_, _, err := r.Run(t.Context(), "keep doing the requested work")
			if err != nil || r.Outcome.Status != outcome.Completed || judgments != 1 || len(model.seen) != 4 {
				t.Fatalf("verdict stopped work: %v %+v judgments=%d requests=%d", err, r.Outcome, judgments, len(model.seen))
			}
			guidance := 0
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "guidance" {
					guidance++
					if entry.Message.Role != "user" || strings.TrimSpace(entry.Message.Content) == "" {
						t.Fatal("empty or uncorrelated guidance entered context")
					}
				}
			}
			if (guidance > 0) != tc.nudge {
				t.Fatalf("unexpected model nudge: %d want=%t", guidance, tc.nudge)
			}
			for i, kind := range sink.kinds {
				if kind == decision.KindJudgment {
					j, ok := decision.ParseJudgment(sink.texts[i])
					if !ok || (j.Resolution == "failed-confidence") != (tc.confidence < .5) {
						t.Fatalf("wrong logged disposition: %+v", j)
					}
				}
			}
			if r.assignedClaim || r.Continuation.Assignment != nil {
				t.Fatal("supervision invented a task claim")
			}
		})
	}
}

func TestSupervisionBookkeepingSilenceDoesNotReissueOldAdvice(t *testing.T) {
	for _, confidence := range []float64{.499, .5, .6, .99} {
		for _, pick := range []int{0, 1} {
			r := &Runner{Prompts: prompts.Builtin(), Threshold: .7, Workspace: "/controlled", Session: &recordSink{},
				Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "root"}},
				Assigned: &AssignedOps{Store: "owner", Facts: func(context.Context) (any, error) {
					return BookkeepingFacts{Preparation: "current legal Q/A", Unfinished: []byte(`{"state":"Draft"}`)}, nil
				}}}
			r.enforcement.latest = &observed{Tool: "read", Result: tool.Result{Text: "actual code"}}
			r.enforcement.repair, r.enforcement.presentation, r.enforcement.prepare = "previous advice", "previous advice", true
			r.enforcement.repairFacts = owningFactsDigest(BookkeepingFacts{Preparation: "current legal Q/A", Unfinished: []byte(`{"state":"Draft"}`)})
			r.Continuation.Add("guidance", "user", "previous retained instruction", false)
			r.Continuation.Entries[0].PreparationScope = r.preparationScope()
			before := r.Continuation.Clone()
			judgments := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Option: q.Options[pick], Confidence: confidence}, nil
			}
			advice, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil })
			nudge := pick != 0 && confidence >= .5
			if err != nil || (advice != "") != nudge {
				t.Fatalf("PAT silence mismatch pick=%d confidence=%g advice=%q err=%v", pick, confidence, advice, err)
			}
			cached, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil })
			if err != nil || cached != "" || judgments != 1 {
				t.Fatalf("cached judgment reissued a nudge: %q %v judgments=%d", cached, err, judgments)
			}
			if !nudge {
				if delivered, err := r.publishPreparationGuidance(t.Context(), advice, r.enforcement.prepare, ""); err != nil || delivered || !reflect.DeepEqual(before, r.Continuation) {
					t.Fatalf("empty current advice rebuilt an instruction: delivered=%t err=%v", delivered, err)
				}
			}
		}
	}
}

func TestSupervisionThinkingVerdictDoesNotTerminateOrRewriteReasoning(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pick       int
		confidence float64
	}{
		{"continue", 1, .99}, {"continue failed confidence", 1, .499},
		{"reconsider failed confidence", 0, .499}, {"reconsider floor", 0, .5}, {"reconsider confident", 0, .9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			judgments := 0
			r := thinkingRunner(func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Pick: tc.pick, Option: q.Options[tc.pick], Confidence: tc.confidence}, nil
			})
			thought := strings.Repeat("abcdefgh", 16) + " then continue my own reasoning"
			r.Continuation.Add("intent", "user", "original task", false)
			before := r.Continuation.Clone()
			reply, err := thinkingFixture(t, r, [][2]string{{"reasoning", thought}}, false, nil)
			if err != nil || reply.Message.Reasoning != thought || reply.Message.Content != "done" || judgments != 1 || !reflect.DeepEqual(before, r.Continuation) {
				t.Fatalf("advice altered generation/context: %v reply=%+v judgments=%d", err, reply, judgments)
			}
		})
	}
}

func TestSupervisionBookkeepingRunnerDoesNotPublishSilentVerdict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pick       int
		confidence float64
	}{
		{"continue", 0, .99}, {"failed-confidence", 1, .499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptModel{replies: []string{strings.Replace(echoBlock("first"), "tool: echo", "tool: read", 1), strings.Replace(echoBlock("second"), "tool: echo", "tool: read", 1)}}
			judgments := 0
			r, _ := loopRunner(model, nil, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Option: q.Options[tc.pick], Confidence: tc.confidence}, nil
			})
			var effects []string
			d := echoTool(&effects)
			d.Name = "read"
			r.Registry = tool.NewRegistry()
			r.Registry.Register(d)
			r.MaxSteps, r.LoopRepeat, r.LoopStall = 2, 0, 0
			r.Workspace, r.Session, r.SessionID = "/controlled", &recordSink{}, "pat-silence"
			r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "root"}
			r.Assigned = &AssignedOps{Store: "owner", Reset: func() {}, Facts: func(context.Context) (any, error) {
				return BookkeepingFacts{Unfinished: []byte(`{"state":"Draft"}`), Preparation: "current legal Q/A"}, nil
			}, Observe: func(tool.Definition, tool.Args, tool.Result, error) {
				// Explicit stale presentation fixture exercises the real Run delivery
				// seam after a valid owner receipt, not a helper-only assertion.
				r.enforcement.repair, r.enforcement.presentation, r.enforcement.prepare = "old repair", "old directive", true
			}}
			_, _, err := r.Run(t.Context(), "inspect current work")
			if err != nil || len(model.seen) != 2 || r.Outcome.Status != outcome.Stopped || judgments != 0 || len(effects) != 2 {
				t.Fatalf("control run did not reach the second request: %v %+v requests=%d judgments=%d effects=%d", err, r.Outcome, len(model.seen), judgments, len(effects))
			}
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "guidance" {
					t.Fatalf("silent judgment published guidance: %q", entry.Message.Content)
				}
			}
			for _, message := range model.seen[1] {
				if message.Role == "user" && (message.Content == "" || strings.Contains(message.Content, "old directive") || strings.Contains(message.Content, "current legal Q/A")) {
					t.Fatalf("old presentation or fabricated Q/A frame reached model: %q", message.Content)
				}
			}
		})
	}
}
