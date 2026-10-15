package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

type attemptRecord struct {
	Kind            string
	Verification    outcome.Verification
	Attempt         int
	Outcome         outcome.Outcome
	Output          string
	Review          string `json:",omitempty"`
	Error           string
	Command         string                  `json:",omitempty"`
	Cwd             string                  `json:",omitempty"`
	Started, Ended  string                  `json:",omitempty"`
	ExitCode        *int                    `json:",omitempty"`
	Acceptance      *session.CommandReceipt `json:",omitempty"`
	Revision        int                     `json:",omitempty"`
	EpisodeStarted  string                  `json:"episode_started,omitempty"`
	EpisodeEnded    string                  `json:"episode_ended,omitempty"`
	CheckEvents     []session.Event         `json:",omitempty"`
	Truncated       bool                    `json:",omitempty"`
	ReviewerEvents  []session.Event         `json:",omitempty"`
	ExecutionEvents []session.Event         `json:",omitempty"`
	Transcript      []llm.Message
}

func (r Result) Qualified() bool {
	command, acceptance := "", ""
	if r.brief != nil {
		command, acceptance = r.brief.CheckCommand, r.brief.AcceptanceCommand
	}
	return (&plan.Task{State: r.State, Execution: r.Outcome.Status, Verification: r.Verification, CheckCommand: command, AcceptanceCommand: acceptance, Attempts: r.Attempts, AttemptLog: r.AttemptLog}).Qualified()
}
func resultOf(t *plan.Task) Result {
	brief := *t
	return Result{brief: &brief, TaskID: t.ID, Title: t.Title, State: t.State, Output: t.Result, Attempts: t.Attempts, Outcome: outcome.Outcome{Status: t.Execution, Reason: t.ExecutionReason}, Verification: t.Verification, VerificationReason: t.VerificationReason, Review: t.Review, AttemptLog: t.AttemptLog, Skipped: true, Reused: true}
}
func admissionFailure(t *plan.Task, err error) Result {
	brief := *t
	return Result{brief: &brief, TaskID: t.ID, Title: t.Title, State: t.State, Output: t.Result, Attempts: t.Attempts, Outcome: outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}, Verification: outcome.Unverified, Err: err, AttemptLog: t.AttemptLog}
}
func settle(s *store.Store, r *Result, contexts ...context.Context) error {
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	// Cancellation withholds new effects; already-settled facts still persist.
	ctx = context.WithoutCancel(ctx)
	if err := plan.PausePendingRevision(ctx, s, r.TaskID, r.Attempts); err != nil {
		return err
	}
	t, err := plan.GetTask(s, r.TaskID)
	if err != nil {
		return err
	}
	t.Result, t.Review, t.Execution, t.ExecutionReason, t.Verification, t.VerificationReason, t.AttemptLog = r.Output, r.Review, r.Outcome.Status, r.Outcome.Reason, r.Verification, r.VerificationReason, r.AttemptLog
	terminal := plan.TaskFailed
	switch r.Outcome.Status {
	case outcome.Completed:
		if r.Verification != outcome.Rejected {
			terminal = plan.TaskDone
		}
	case outcome.Stopped:
		terminal = plan.TaskStopped
	case outcome.Cancelled:
		terminal = plan.TaskCancelled
	}
	var expected []*plan.Task
	if r.brief != nil {
		e := *r.brief
		e.State, e.Attempts = r.State, r.Attempts
		expected = append(expected, &e)
	}
	if err := plan.SettleTaskOutcome(s, t, terminal, expected...); err != nil {
		return err
	}
	r.State = terminal
	return nil
}

type Counts struct{ Qualified, Executed, Verified, Unverified, Failed, Stopped, Cancelled, Queued int }

func countResults(rs []Result) Counts {
	var c Counts
	for _, r := range rs {
		if r.Qualified() {
			c.Qualified++
		}
		if r.Outcome.Status == outcome.Completed {
			c.Executed++
			switch r.Verification {
			case outcome.Passed:
				if r.Qualified() {
					c.Verified++
				}
			}
		}
		if r.State == plan.TaskDone && r.Verification == outcome.Unverified {
			c.Unverified++
		}
		if r.State == plan.TaskQueued {
			c.Queued++
		}
		switch r.Outcome.Status {
		case outcome.Failed:
			c.Failed++
		case outcome.Stopped:
			c.Stopped++
		case outcome.Cancelled:
			c.Cancelled++
		}
		if r.Verification == outcome.Rejected {
			c.Failed++
		}
	}
	return c
}

// WorkflowReport reads the actual owning nodes, including every reused task.
// A refused attempt remains unresolved even if prior raw state said complete.
func WorkflowReport(d Deps, id string, attemptErr error) (*outcome.Report, error) {
	r := &outcome.Report{Ref: id, Outcome: outcome.Outcome{Status: outcome.Failed}}
	if attemptErr != nil {
		if errors.Is(attemptErr, context.Canceled) {
			r.Outcome.Status = outcome.Cancelled
		}
		r.Outcome.Reason = attemptErr.Error()
		return r, nil
	}
	a, err := plan.GetApproach(d.Store, id)
	if err != nil {
		r.Outcome.Reason = err.Error()
		return r, err
	}
	tasks, err := plan.ApproachTasks(d.Store, id)
	if err != nil {
		r.Outcome.Reason = err.Error()
		return r, err
	}
	all := len(tasks) > 0
	for _, t := range tasks {
		if !t.Qualified() {
			all = false
		}
		reviewStatus := outcome.Unknown
		if t.Verification == outcome.Unverified && t.AttemptLog != "" {
			var records []attemptRecord
			if err := json.Unmarshal([]byte(t.AttemptLog), &records); err != nil {
				r.Outcome.Reason = "cannot read attempt evidence: " + err.Error()
				return r, err
			}
			for i := len(records) - 1; i >= 0; i-- {
				if records[i].Kind == "review" || records[i].Kind == "check" {
					reviewStatus = records[i].Outcome.Status
					break
				}
			}
		}
		if t.Execution == outcome.Cancelled || reviewStatus == outcome.Cancelled {
			r.Outcome.Status = outcome.Cancelled
		} else if (t.Execution == outcome.Stopped || reviewStatus == outcome.Stopped) && r.Outcome.Status != outcome.Cancelled {
			r.Outcome.Status = outcome.Stopped
		}
	}
	if (a.State == plan.ApproachComplete || a.State == plan.ApproachRetired) && all {
		r.Qualified = true
		r.Outcome = outcome.Outcome{Status: outcome.Completed}
		return r, nil
	}
	if a.State == plan.ApproachPaused && r.Outcome.Status == outcome.Failed {
		r.Outcome.Status = outcome.Stopped
	}
	r.Outcome.Reason = fmt.Sprintf("approach %s is %s with incomplete or unverified work", id, a.State)
	for _, t := range tasks {
		if !t.Qualified() && t.Verification == outcome.Passed && !t.Checked() {
			r.Outcome.Reason += "; model PASS lacks matching check/review receipts; explicit rework required"
		}
		if !t.Qualified() && t.VerificationReason != "" {
			r.Outcome.Reason += "; " + t.ID + ": " + t.VerificationReason
		}
		if t.State == plan.TaskDone && t.Execution == outcome.Unknown {
			r.Outcome.Reason += "; legacy execution needs explicit done→failed→queued rework; a reviewer cannot prove it ran"
			break
		}
	}
	return r, nil
}

// workflowResult never derives success from a model's prose or raw lifecycle alone.
func workflowResult(d Deps, id string, results []Result, attemptErr error) (tool.Result, error) {
	report, err := WorkflowReport(d, id, attemptErr)
	text := report.Outcome.Reason
	if results != nil {
		text = format(id, results, d.Sub.MaxResultChars)
		if a, readErr := plan.GetApproach(d.Store, id); readErr == nil {
			text = fmt.Sprintf("approach %s is %s\n", id, a.State) + text
		}
	}
	return tool.Result{Text: text, Failed: !report.Qualified, Workflow: report}, errors.Join(attemptErr, err)
}

func appendReview(r *Result, record attemptRecord) error {

	log, err := outcome.AppendRecord(r.AttemptLog, record)
	if err == nil {
		r.AttemptLog = log
	}
	return err
}

// popPending removes only this attempt's owned transient record. Other raw records
// retain their fields and bytes, including user comments and future extensions.
func popPending(r *Result, kind string) (attemptRecord, error) {
	var records []json.RawMessage
	if err := json.Unmarshal([]byte(r.AttemptLog), &records); err != nil {
		return attemptRecord{}, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		var pending attemptRecord
		if err := json.Unmarshal(records[i], &pending); err != nil {
			return attemptRecord{}, err
		}
		if pending.Kind != kind || pending.Attempt != r.Attempts {
			continue
		}
		log := "[]"
		for j, raw := range records {
			if j == i {
				continue
			}
			var err error
			log, err = outcome.AppendRecord(log, raw)
			if err != nil {
				return attemptRecord{}, err
			}
		}
		r.AttemptLog = log
		return pending, nil
	}
	return attemptRecord{}, nil
}
