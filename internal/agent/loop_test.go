package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/outcome"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

var errLoopDown = errors.New("sidecar down")

func echoBlock(text string) string {
	return "```tool\ntool: echo\ntext: " + text + "\n```"
}

func loopRunner(model Model, exec Exec, judge JudgeFunc) (*Runner, *[]string) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	if exec == nil {
		exec = Direct
	}
	return &Runner{Model: model, Registry: reg, Exec: exec, MaxSteps: 10,
		Judge: judge, Threshold: 0.7, LoopRepeat: 3, LoopStall: 5}, &calls
}

func stubLoopJudge(v decision.Verdict, err error) JudgeFunc {
	return func(context.Context, decision.Request) (decision.Verdict, error) { return v, err }
}

func runLoop(t *testing.T, r *Runner) string {
	t.Helper()
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	return final
}

// Scripted completion, actual Runner/cadence/tool execution. This changes only
// progress advice, not authority or transport/cancellation handling.
func TestProgressCadenceNeverStopsOnVerdict(t *testing.T) {
	for _, kind := range []string{"repeat", "recoverable errors", "refused calls", "blocked", "failed results"} {
		for _, confidence := range []float64{.4, .7, .99} {
			t.Run(fmt.Sprintf("%s/%.2f", kind, confidence), func(t *testing.T) {
				replies := []string{echoBlock("same"), echoBlock("same"), echoBlock("same"), echoBlock("same"), echoBlock("same"), echoBlock("same"), "final answer"}
				var execute Exec
				switch kind {
				case "recoverable errors":
					execute = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
						return tool.Result{}, errors.New("recoverable tool failure; fix the named argument")
					}
				case "refused calls":
					for i := 0; i < 6; i++ {
						replies[i] = "```tool\ntool: unknown\nx: 1\n```"
					}
				case "blocked":
					execute = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
						return tool.Result{Blocked: true, Text: "denied; approval still required"}, nil
					}
				case "failed results":
					execute = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
						return tool.Result{Failed: true, Text: "actual failed operation"}, nil
					}
				}
				model := &scriptModel{replies: replies}
				judgments := 0
				r, _ := loopRunner(model, execute, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
					judgments++
					if strings.Contains(q.Context, "no observed progress") {
						t.Error("cadence assertion biases the judgment")
					}
					pick := 0
					if len(q.Options) == 3 {
						pick = 2
					} // The third direction deliberates about a different grounded method.
					return decision.Verdict{Option: q.Options[pick], Confidence: confidence}, nil
				})
				r.Ask = func(context.Context, string) bool {
					t.Fatal("progress advice requested permission to continue")
					return false
				}
				final, _, err := r.Run(t.Context(), "repair a real requested behavior")
				if err != nil || final != "final answer" || r.Outcome.Status != outcome.Completed || len(model.seen) != 7 || judgments == 0 {
					t.Fatalf("progress verdict became a ceiling: final=%q outcome=%+v replies=%d judgments=%d err=%v", final, r.Outcome, len(model.seen), judgments, err)
				}
				joined := ""
				for _, msg := range model.seen[6] {
					joined += msg.Content
				}
				if strings.Contains(joined, r.Prompts.Text("continuation.discovery.reconsider")) != (confidence >= .5) {
					t.Fatal("direction ignored the owner's advisory confidence floor")
				}
			})
		}
	}
}

func TestProgressCadenceLogsRealAdvisoryDisposition(t *testing.T) {
	m := &scriptModel{replies: []string{echoBlock("hi"), echoBlock("hi"), echoBlock("hi"), "final"}}
	r, _ := loopRunner(m, nil, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		return decision.Verdict{Option: q.Options[2], Confidence: .99}, nil
	})
	sink := &recordSink{}
	r.Session = sink
	r.SessionID = "actual-component"
	if final := runLoop(t, r); final != "final" {
		t.Fatal(final)
	}
	judgments := 0
	for i, k := range sink.kinds {
		if k == decision.KindJudgment {
			j, ok := decision.ParseJudgment(sink.texts[i])
			if !ok || j.Source != decision.SourceLoopConfirm || j.Resolution != "reconsider-approach" {
				t.Fatalf("bad real disposition: %+v", j)
			}
			judgments++
		}
	}
	if judgments != 1 {
		t.Fatalf("judgments=%d", judgments)
	}
}

func TestProgressCadenceRecordingFailureRemainsLoud(t *testing.T) {
	for _, failure := range []string{"judgment receipt", "guidance receipt"} {
		t.Run(failure, func(t *testing.T) {
			m := &scriptModel{replies: []string{echoBlock("hi"), echoBlock("hi"), echoBlock("hi"), "must not continue"}}
			r, _ := loopRunner(m, nil, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				return decision.Verdict{Option: q.Options[2], Confidence: .99}, nil
			})
			sink := &discoveryFailureSink{recordSink: &recordSink{}}
			if failure == "judgment receipt" {
				sink.fail = "judgment"
			}
			if failure == "guidance receipt" {
				sink.fail = "guidance"
			}
			r.Session, r.SessionID = sink, "failure"
			_, _, err := r.Run(t.Context(), "go")
			if err == nil || r.Outcome.Status != outcome.Failed || len(m.seen) != 3 {
				t.Fatalf("dependency/durability failure hidden: %+v %v", r.Outcome, err)
			}
		})
	}
}

func TestProgressCadenceNoJudgeOrDisabledStaysQuiet(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m := &scriptModel{replies: []string{echoBlock("hi"), echoBlock("hi"), echoBlock("hi"), "done"}}
		var judge JudgeFunc
		if enabled {
			judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				t.Fatal("disabled cadence judged")
				return decision.Verdict{}, nil
			}
		}
		r, _ := loopRunner(m, nil, judge)
		r.LoopRepeat, r.LoopStall = 0, 0
		if final := runLoop(t, r); final != "done" {
			t.Fatal(final)
		}
	}
}

func TestSuccessfulUserQuestionsRemainProgressWithPendingPAT(t *testing.T) {
	r := &Runner{Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
		return BookkeepingFacts{Unfinished: json.RawMessage(`{"id":"selected","state":"draft"}`)}, nil
	}}}
	r.enforcement.repair = "current task brief still needs authoring"
	ctx := t.Context()
	r.bookkeepingProgress(ctx, true)
	execute := r.assignmentExec(func(_ context.Context, d tool.Definition, a tool.Args) (tool.Result, error) {
		return tool.Result{Text: a["answer"].(string)}, nil
	})
	for _, answer := range []string{"20×20 board", "dark theme", "speed every five foods", "visible touch buttons", "swipe support", "silent game"} {
		_, err := execute(ctx, tool.Definition{Name: "ask"}, tool.Args{"answer": answer})
		if err != nil || !r.bookkeepingProgress(ctx, true) || r.enforcement.current == nil || r.enforcement.current.Tool != "ask" || r.enforcement.current.Result.Text != answer {
			t.Fatalf("actual user answer erased by pending PAT: %q", answer)
		}
	}
	if r.enforcement.repair != "current task brief still needs authoring" || r.assignedClaim {
		t.Fatal("observation fabricated completion or claim")
	}
}
