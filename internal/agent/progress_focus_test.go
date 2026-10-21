package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Exact identities/state shape from native D1 packets 74/78. The owner facts
// are reconstructed from their actual next-question states, not a new claim.
func TestProgressFocusOutlivesPriorSettlement(t *testing.T) {
	const oldTask = "7578ee8d-be04-433f-99c1-cc9602629a38"
	facts := BookkeepingFacts{}
	r := &Runner{Prompts: prompts.Builtin(), Workspace: t.TempDir(), assignedClaim: true,
		Assigned:   &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }},
		settlement: &TaskSettlement{TaskID: oldTask, Attempt: 1, State: "done", Qualified: true, Check: outcome.Passed, Verification: outcome.Passed}}
	r.Continuation.Assignment = &session.Assignment{Store: "recorded-owner", Workspace: r.Workspace, TaskID: oldTask, Attempt: 1}
	for _, tc := range []struct{ name, kind, id, step, next string }{
		{"packet74", "approach", "398cb3ce-b715-4ed4-8c41-659b2a112dd1", "approach-draft", "DRAFT TASK: supply title and objective"},
		{"packet78", "task", "050aa9da-8542-41f1-8f22-e28431df48e9", "task-queued", "DRAFT OBSERVATIONS: current and target"},
		{"back-to-plan", "plan", "ad24c2f4-dba9-4aa2-8995-4063acfe5193", "plan-active", "DRAFT APPROACH: select remaining coverage"},
		{"back-to-old-task", "task", oldTask, "task-done", "Task qualified; NEXT inspects its Approach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.Continuation.Workflow = &session.WorkflowScope{Store: "recorded-owner", Kind: tc.kind, ID: tc.id}
			facts = BookkeepingFacts{Preparation: tc.next, Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: tc.step, SelectedID: tc.id, MissingFact: "unknown"}}
			before, _ := json.Marshal([]any{r.Continuation, r.settlement, facts})
			p, err := r.progressEvidence(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			wantPhase, wantStep := "PREPARE", tc.step
			if tc.id == oldTask {
				wantPhase, wantStep = "HANDOFF", "settled-done"
			}
			if p.Checkpoint.Phase != wantPhase || p.Checkpoint.PATStep != wantStep || p.Checkpoint.SelectedID != tc.id || p.Checkpoint.NextAction != tc.next || p.CurrentClaim {
				t.Fatalf("historical settlement became current work: checkpoint=%+v currentClaim=%t", p.Checkpoint, p.CurrentClaim)
			}
			// Inspect the wire so baseline still compiles before the field exists.
			raw, _ := json.Marshal(p)
			var wire struct{ HistoricalSettlement *TaskSettlement }
			if err := json.Unmarshal(raw, &wire); err != nil || !reflect.DeepEqual(wire.HistoricalSettlement, r.settlement) {
				t.Fatalf("historical outcome disappeared: %s %v", raw, err)
			}
			after, _ := json.Marshal([]any{r.Continuation, r.settlement, facts})
			if string(before) != string(after) {
				t.Fatal("advisory projection mutated owning history")
			}
		})
	}
}

func TestProgressNewClaimReplacesSettlementAndTracksChecks(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Workspace: t.TempDir(), Session: &recordSink{}, SessionID: "focus", assignedClaim: true, Outcome: outcome.Outcome{Status: outcome.Unknown},
		settlement: &TaskSettlement{TaskID: "old", Attempt: 1, State: "done", Qualified: true}}
	r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "task", ID: "next"}
	r.Continuation.Version = 1
	r.Continuation.Add("intent", "user", "complete the selected task", false)
	r.Continuation.Assignment = &session.Assignment{Store: "owner", Workspace: r.Workspace, TaskID: "old", Attempt: 1}
	r.Assigned = &AssignedOps{
		Start: func(context.Context, string) (*session.Assignment, string, error) {
			return &session.Assignment{Store: "owner", Workspace: r.Workspace, PlanID: "plan", ApproachID: "approach", TaskID: "next", Attempt: 2}, "project-check", nil
		},
		AcceptanceCommand: func() string { return "task-check" },
		Facts:             func(context.Context) (any, error) { return BookkeepingFacts{}, nil },
	}
	if result, err := r.TaskStartDefinition().Execute(t.Context(), tool.Args{"task": "next"}); err != nil || result.Failed {
		t.Fatalf("existing real claim owner failed: %+v %v", result, err)
	}
	p, err := r.progressEvidence(t.Context(), nil)
	if err != nil || !p.CurrentClaim || p.HistoricalSettlement != nil || p.Checkpoint.SelectedID != "next" || p.Checkpoint.Phase != "EXECUTE" {
		t.Fatalf("new claim kept historical settlement: %+v %v", p, err)
	}
	c, zero := r.Continuation.Coding, 0
	c.Receipt = &session.CheckReceipt{Command: c.Command, Cwd: c.Workspace, Revision: c.Revision, Started: "start", Ended: "end", EpisodeStarted: "start", EpisodeEnded: "end", Status: "passed", ExitCode: &zero,
		Acceptance: &session.CommandReceipt{Command: c.AcceptanceCommand, Cwd: c.Workspace, Started: "start", Ended: "end", Status: "passed", ExitCode: &zero}}
	p, err = r.progressEvidence(t.Context(), nil)
	if err != nil || !p.CurrentClaim || p.Checkpoint.Phase != "VALIDATE" || p.Checkpoint.SelectedID != "next" {
		t.Fatalf("current real paired-check state missing: %+v %v", p, err)
	}
	r.settlement = &TaskSettlement{TaskID: "next", Attempt: 2, State: "paused", Verification: outcome.Rejected, Feedback: "current review rejected"}
	p, err = r.progressEvidence(t.Context(), nil)
	if err != nil || p.CurrentClaim || p.Checkpoint.Phase != "REWORK" || p.Checkpoint.SelectedID != "next" || p.HistoricalSettlement.Feedback != "current review rejected" {
		t.Fatalf("rejected current task lost rework: %+v %v", p, err)
	}
}

func TestProgressHistoricalReviewCapacityPreservesSettlement(t *testing.T) {
	for _, currentFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "historical", true: "current-failure"}[currentFailure], func(t *testing.T) {
			selected := "new-task"
			if currentFailure {
				selected = "old-task"
			}
			s := &TaskSettlement{TaskID: "old-task", Attempt: 1, State: "paused", Verification: outcome.Rejected, Feedback: strings.Repeat("recorded reviewer feedback; ", 2000)}
			original, _ := json.Marshal(s)
			r := &Runner{Prompts: prompts.Builtin(), settlement: s, assignedClaim: true,
				Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "owner", Kind: "task", ID: selected}},
				Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
					return BookkeepingFacts{Preparation: "current owner action", Checkpoint: ProgressCheckpoint{Phase: "PREPARE", SelectedID: selected}}, nil
				}}}
			calls := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				calls++
				if calls == 1 {
					return decision.Verdict{}, &decision.ContextCapacityError{Message: "existing admission"}
				}
				var wire progressPacket
				if err := json.Unmarshal([]byte(q.Context), &wire); err != nil {
					t.Fatal(err)
				}
				if wire.HistoricalSettlement.Feedback != "" || wire.HistoricalSettlement.TaskID != s.TaskID || wire.HistoricalSettlement.Verification != s.Verification || len(wire.CapacityOmissions) != 1 || wire.Checkpoint.SelectedID != selected {
					t.Fatalf("lost current focus/settlement provenance: %+v", wire)
				}
				return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
			}
			advice, err := r.confirmLoop(t.Context(), nil, "existing cadence", func(string, string) error { return nil }, "")
			wantCalls := 2
			if currentFailure {
				wantCalls = 1
			}
			if err != nil || advice != "" || calls != wantCalls {
				t.Fatalf("advisory semantics changed: %q %v calls=%d", advice, err, calls)
			}
			after, _ := json.Marshal(s)
			if string(original) != string(after) {
				t.Fatal("D1 capacity mutated raw settlement")
			}
		})
	}
}

func TestProgressFocusResumeDoesNotClaimHistoricalAssignment(t *testing.T) {
	r := &Runner{Prompts: prompts.Builtin(), Workspace: t.TempDir(), Assigned: &AssignedOps{}}
	r.Continuation.Assignment = &session.Assignment{Store: "owner", Workspace: r.Workspace, TaskID: "historical", Attempt: 1}
	r.Continuation.Workflow = &session.WorkflowScope{Store: "owner", Kind: "task", ID: "selected"}
	facts := BookkeepingFacts{Preparation: "Draft current task", Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: "task-queued", SelectedID: "selected"}}
	r.Assigned.Facts = func(context.Context) (any, error) { return facts, nil }
	p, err := r.progressEvidence(t.Context(), nil)
	if err != nil || p.CurrentClaim || p.Checkpoint.SelectedID != "selected" || p.Checkpoint.Phase != "PREPARE" {
		t.Fatalf("resume invented execution: %+v %v", p, err)
	}
}
