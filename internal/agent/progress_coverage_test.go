package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Scripted model and judge; actual Bash and correlated native settlement.
func TestProgressCoverageBashOnlyNativeBoundary(t *testing.T) {
	for _, verdict := range []struct {
		name       string
		pick       int
		confidence float64
		guidance   bool
	}{
		{"continue", 0, .99, false}, {"uncertain", 1, .499, false}, {"reconsider", 2, .5, true},
	} {
		t.Run(verdict.name, func(t *testing.T) {
			m := &discoveryModel{}
			for i := 0; i < 4; i++ {
				m.replies = append(m.replies, progressBashReply(i))
			}
			// Review must wait until both calls, including the sixth receipt, settle.
			last := progressBashReply(4)
			last.Message.ToolCalls = append(last.Message.ToolCalls, progressBashReply(5).Message.ToolCalls...)
			last.Message.ReasoningContent = "Completed inspection; retain this exact reasoning in model context."
			m.replies = append(m.replies, last)
			sink, judgments := &recordSink{}, 0
			r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				var p progressPacket
				if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
					t.Fatal(err)
				}
				if len(m.seen) != 5 || p.LatestSettled == nil || p.LatestSettled.CallID != "bash-5" || len(p.RecentReceipts) != 5 || p.Checkpoint.RunStep != 6 {
					t.Fatalf("judgment preceded native array settlement: requests=%d packet=%+v", len(m.seen), p)
				}
				if p.PlanningActive || p.CurrentClaim || p.Assigned || p.Checkpoint.Phase != "EXECUTE" || p.Checkpoint.PATStep != "not-selected" || p.LatestSourceText != "" || p.OriginalUserIntent != "inspect relevant files and implement the requested change" {
					t.Fatalf("ordinary Bash evidence fabricated planning/source/claim: %+v", p)
				}
				for _, receipt := range p.RecentReceipts {
					if receipt.Tool != "bash" || receipt.ExitCode == nil || *receipt.ExitCode != 0 || !strings.Contains(receipt.OutputText, "actual source") {
						t.Fatalf("missing actual Bash result: %+v", receipt)
					}
				}
				return decision.Verdict{Option: q.Options[verdict.pick], Confidence: verdict.confidence}, nil
			}, sink)
			r.Registry.Register(bash.NewAt(config.Defaults().Tools.Bash, r.Workspace))
			_, _, err := r.Run(t.Context(), "inspect relevant files and implement the requested change")
			if err != nil || r.Outcome.Status != outcome.Completed || judgments != 1 || len(m.seen) != 6 {
				t.Fatalf("Bash checkpoint coverage: %v outcome=%+v judgments=%d requests=%d", err, r.Outcome, judgments, len(m.seen))
			}
			guidance, reasoning := 0, false
			for _, msg := range m.seen[5] {
				reasoning = reasoning || msg.ReasoningContent == last.Message.ReasoningContent
			}
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "guidance" {
					guidance++
				}
			}
			if !reasoning || (guidance > 0) != verdict.guidance || r.PlanningActive() || r.assignedClaim {
				t.Fatalf("advice changed ownership/reasoning or violated silence: reasoning=%t guidance=%d", reasoning, guidance)
			}
		})
	}
}

func progressBashReply(i int) llm.Reply {
	raw, _ := json.Marshal(tool.Args{"cmd": fmt.Sprintf("cat %d", i)})
	return llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: fmt.Sprint("bash-", i), Name: "bash", Arguments: string(raw)}}}, FinishReason: "tool_calls"}
}

func TestProgressCoverageFailedBashReceipts(t *testing.T) {
	w, judgments := &watcher{}, 0
	r := &Runner{Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "failed-Bash", LoopStall: 5}
	r.Continuation.Add("intent", "user", "repair the actual command failures", false)
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var p progressPacket
		if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
			t.Fatal(err)
		}
		// First judgment is the 3-straight-failures trigger, second the
		// cadence backstop at 5; both must carry exact failure provenance.
		want := []int{3, 5}[judgments-1]
		if len(p.RecentReceipts) != want || p.LatestSettled == nil || p.LatestSettled.ExitCode == nil || *p.LatestSettled.ExitCode != want {
			t.Fatalf("missing settled failure provenance: %+v", p)
		}
		for i, receipt := range p.RecentReceipts {
			if receipt.ExitCode == nil || *receipt.ExitCode != i+1 || !receipt.Failed || !strings.Contains(receipt.OutputText, fmt.Sprint("actual failure ", i+1)) {
				t.Fatalf("failure became progress: %+v", receipt)
			}
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}
	execute := r.assignmentExec(Direct)
	d := bash.New(config.Defaults().Tools.Bash)
	for i := 1; i <= 5; i++ {
		args := tool.Args{"cmd": fmt.Sprintf("printf 'actual failure %d'; exit %d", i, i), "timeout": int64(30)}
		result, err := execute(t.Context(), d, args)
		if err != nil || result.ExitCode == nil || *result.ExitCode != i {
			t.Fatalf("actual Bash fixture failed: %+v %v", result, err)
		}
		r.enforcement.current.CallID = fmt.Sprint("failed-", i)
		w.completedDiscovery(llm.Message{Role: "assistant", Content: fmt.Sprint("attempt ", i)}, 5)
		w.discoveryReceipt(r.enforcement.current, 5)
		advice, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil })
		wantJudgments := 0
		if i >= 3 {
			wantJudgments++
		}
		if i >= 5 {
			wantJudgments++
		}
		if err != nil || advice != "" || judgments != wantJudgments {
			t.Fatalf("failed receipts bypassed checkpoint: attempt=%d judgments=%d advice=%q err=%v", i, judgments, advice, err)
		}
	}
}

func TestProgressCoveragePhaseAndGraphChanges(t *testing.T) {
	w, judgments := &watcher{}, 0
	facts := BookkeepingFacts{Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: "task-brief", SelectedID: "current"}}
	r := &Runner{Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "phase", LoopStall: 5,
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "actual", Kind: "task", ID: "current"}},
		Assigned:     &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		var p progressPacket
		if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
			t.Fatal(err)
		}
		if judgments == 1 && p.Checkpoint.Phase != "PREPARE" || judgments == 2 && p.Checkpoint.Phase != "EXECUTE" {
			t.Fatalf("judge received wrong real phase: %+v", p.Checkpoint)
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
	}
	for i := 1; i <= 5; i++ {
		// A changing authored record is evidence, not a new lifecycle phase.
		facts.Unfinished = json.RawMessage(fmt.Sprintf(`{"authored_answer":%d}`, i))
		w.discoveryReceipt(&observed{Tool: "graphql", CallID: fmt.Sprint(i), Result: tool.Result{Text: "actual authored graph receipt", Changed: true}}, 5)
		if advice, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil }); err != nil || advice != "" || judgments != i/5 {
			t.Fatalf("graph writes postponed or forced review: receipt=%d judgments=%d advice=%q err=%v", i, judgments, advice, err)
		}
	}
	facts.Checkpoint.Phase, facts.Checkpoint.PATStep = "EXECUTE", "claimed"
	w.discoveryReceipt(&observed{Tool: "task_start", CallID: "start", Result: tool.Result{Text: "actual claim receipt"}}, 5)
	if advice, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil }); err != nil || advice != "" || judgments != 2 {
		t.Fatalf("phase change did not receive advisory review: judgments=%d advice=%q err=%v", judgments, advice, err)
	}
	if _, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil }); err != nil || judgments != 2 {
		t.Fatalf("unchanged phase repeated review: %d %v", judgments, err)
	}
}

func TestProgressCoverageOrdinaryOwnerDoesNotImplyClaim(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
		t.Fatal("ordinary mode fetched unselected or historical PAT advice")
		return nil, nil
	}}, Continuation: session.Continuation{Assignment: &session.Assignment{TaskID: "historical"}}}
	p, err := r.progressEvidence(t.Context(), nil)
	if err != nil || p.Assigned || p.CurrentClaim || p.PlanningActive || p.Facts != nil || p.Checkpoint.Phase != "EXECUTE" || p.Checkpoint.PATStep != "not-selected" {
		t.Fatalf("registered owner or old history became active task instructions: %+v err=%v", p, err)
	}
	if _, err := r.enforce(t.Context(), true, "ordinary final", func(string, string) error { return nil }); err != nil {
		t.Fatalf("ordinary coding acquired mandatory bookkeeping: %v", err)
	}
}

func TestProgressCoveragePendingPlanningCannotBorrowOrdinaryExecution(t *testing.T) {
	refused := errors.New("current planning task has not been claimed")
	r := &Runner{pendingFocus: &session.WorkflowScope{Store: "actual", Kind: "plan", ID: "new-plan"},
		Assigned: &AssignedOps{Admit: func(context.Context) error { return refused }}}
	effects := 0
	_, err := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
		effects++
		return tool.Result{Text: "must not execute"}, nil
	})(t.Context(), tool.Definition{Name: "write"}, tool.Args{})
	if err == nil || !strings.Contains(err.Error(), "selected work has no ordinary project coding admission") || effects != 0 || !r.PlanningActive() || r.assignedClaim {
		t.Fatalf("actual pending scope borrowed prior ordinary admission: err=%v effects=%d active=%t claimed=%t", err, effects, r.PlanningActive(), r.assignedClaim)
	}
}

func TestProgressCoverageCoincidentRepeatAndPhaseReviewedOnce(t *testing.T) {
	for _, v := range []struct {
		name       string
		pick       int
		confidence float64
	}{
		{"continue", 0, .99}, {"uncertain", 1, .499}, {"nudge", 1, .9},
	} {
		t.Run(v.name, func(t *testing.T) {
			m, steps, boundaries := &discoveryModel{}, map[int]int{}, 0
			for i := 0; i < 5; i++ {
				m.replies = append(m.replies, llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: fmt.Sprint("check-", i), Name: "check", Arguments: `{}`}}}, FinishReason: "tool_calls"})
			}
			r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				var p progressPacket
				if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
					t.Fatal(err)
				}
				if p.UnfinishedResponse != nil {
					boundaries++
					if p.UnfinishedResponse.Text != "new unfinished literal reasoning" {
						t.Fatal("new output-boundary evidence was altered")
					}
					return decision.Verdict{Option: q.Options[v.pick], Confidence: v.confidence}, nil
				}
				step := p.Checkpoint.RunStep
				steps[step]++
				wantPhase, wantCode := "EXECUTE", 1
				if step == 4 {
					wantPhase, wantCode = "VALIDATE", 0
				}
				if p.Checkpoint.Phase != wantPhase || p.LatestSettled == nil || p.LatestSettled.ExitCode == nil || *p.LatestSettled.ExitCode != wantCode {
					t.Fatalf("new check/failure evidence lost: step=%d phase=%s receipt=%+v", step, p.Checkpoint.Phase, p.LatestSettled)
				}
				return decision.Verdict{Option: q.Options[v.pick], Confidence: v.confidence}, nil
			}, &recordSink{})
			r.Prompts, r.CodingRequired = prompts.Builtin(), true
			// Actual checks pass twice, fail at the repeat/phase boundary, then
			// pass and fail again. Later novel phase evidence must still be judged.
			r.CheckCommand = `n=$(cat counter 2>/dev/null || printf 0); n=$((n+1)); printf '%s' "$n" >counter; test "$n" -ne 3 && test "$n" -ne 5`
			r.Registry.Register(r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, r.Workspace)))
			if _, _, err := r.Run(t.Context(), "run the declared check and preserve actual failures"); err != nil {
				t.Fatal(err)
			}
			if len(steps) != 3 || steps[3] != 1 || steps[4] != 1 || steps[5] != 1 {
				t.Fatalf("duplicate review or suppression of new evidence: judgments=%v", steps)
			}
			r.ThinkingLoop.SampleBytes = 4096
			partial := llm.Message{Role: "assistant", Reasoning: "new unfinished literal reasoning"}
			if _, err := r.reviewDiscovery(t.Context(), r.progressWatch, func(string, string) error { return nil }, partial); err != nil || boundaries != 1 || partial.Reasoning != "new unfinished literal reasoning" {
				t.Fatalf("reviewed phase suppressed or altered new output boundary: boundaries=%d err=%v", boundaries, err)
			}
		})
	}
}
