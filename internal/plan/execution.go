package plan

import (
	"context"
	"encoding/json"
	"fmt"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
)

// ExecutionContract is a host-captured running attempt. Restored conversation
// provenance is never used to construct one. Capturing does not claim or retry.
type ExecutionContract struct {
	Report                                          checkreport.Contract
	Task                                            *Task
	PlanID, ApproachID, ProjectRoot, Goal, Strategy string
	dispatched                                      bool
	reviewing                                       bool
	approachAtCapture                               string
}

// A dispatched worker already owns its running attempt. Pause withholds queued
// work but lets it settle; unlike inline task_start, launch can use a Draft Plan.
func CaptureDispatchedExecution(ctx context.Context, s *store.Store, expected *Task) (contract *ExecutionContract, err error) {
	return captureExecution(ctx, s, expected, false)
}

func CaptureReviewExecution(ctx context.Context, s *store.Store, expected *Task) (*ExecutionContract, error) {
	return captureExecution(ctx, s, expected, true)
}

func captureExecution(ctx context.Context, s *store.Store, expected *Task, reviewing bool) (contract *ExecutionContract, err error) {
	if expected == nil {
		return nil, fmt.Errorf("execution needs a current running task")
	}
	err = s.UpdateContext(ctx, func(tx *store.Tx) error {
		p, a, t, e := contractAncestry(tx, "task", expected.ID)
		if e != nil {
			return e
		}
		frozen := *expected
		report := executionReportContext(ctx)
		if err := freezeExecutionReport(tx, t, report); err != nil {
			return err
		}
		frozen.AttemptLog = t.AttemptLog
		contract = &ExecutionContract{Task: &frozen, Report: report.Clone(), PlanID: p.ID, ApproachID: a.ID,
			ProjectRoot: p.ProjectRoot, Goal: p.Goal, Strategy: a.Title, dispatched: true, reviewing: reviewing, approachAtCapture: a.State}
		return contract.validate(ctx, tx, p, a, t)
	})
	if err != nil {
		contract = nil
	}
	return
}

// Admit rechecks one snapshot of the current task and all its ancestors. The
// existing revision boundary commits its pause before this readmission check.
func (c *ExecutionContract) Admit(ctx context.Context, s *store.Store) error {
	if c == nil || c.Task == nil {
		return fmt.Errorf("execution has no captured task contract")
	}
	if err := PausePendingRevision(ctx, s, c.Task.ID, c.Task.Attempts); err != nil {
		return err
	}
	return s.UpdateContext(ctx, func(tx *store.Tx) error {
		p, a, t, err := contractAncestry(tx, "task", c.Task.ID)
		if err != nil {
			return err
		}
		return c.validate(ctx, tx, p, a, t)
	})
}

func (c *ExecutionContract) validate(ctx context.Context, s stateStore, p *Plan, a *Approach, t *Task) error {
	if err := CheckProject(s, "task", t.ID, ProjectRoot(ctx)); err != nil {
		return err
	}
	if err := requireNoRevision(s, "task", t.ID); err != nil {
		return err
	}
	if report, found, err := TaskCheckReport(t); err != nil || !found || !report.Equal(c.Report) {
		return fmt.Errorf("attempt check contract changed; no execution admitted")
	}
	planOpen := p.State == PlanActive || c.dispatched && p.State == PlanDraft
	approachOpen := a.State == ApproachRunning || c.dispatched && a.State == ApproachPaused
	// Direct runOne dispatch can claim from Ready. A later reset from Running to
	// Ready is not that original admission and cannot revive its worker.
	approachOpen = approachOpen || c.dispatched && c.approachAtCapture == ApproachReady && a.State == ApproachReady
	taskOpen := t.State == c.Task.State && (t.State == TaskRunning || c.reviewing && t.State == TaskDone)
	if !taskOpen || t.Attempts < 1 || t.Attempts != c.Task.Attempts || !t.SameBrief(c.Task) ||
		a.ID != c.ApproachID || !approachOpen || a.NeedsReady || a.Title != c.Strategy ||
		p.ID != c.PlanID || !planOpen || p.ProjectRoot != c.ProjectRoot || p.Goal != c.Goal {
		return fmt.Errorf("task/brief/attempt/ancestry changed; no execution admitted")
	}
	return ctx.Err()
}

type executionReportKey struct{}

// WithExecutionReport supplies host configuration at claim/capture. The claim
// transaction persists it; model-authored task changes cannot replace it.
func WithExecutionReport(ctx context.Context, report checkreport.Contract) context.Context {
	return context.WithValue(ctx, executionReportKey{}, report.Clone())
}

func executionReportContext(ctx context.Context) checkreport.Contract {
	report, _ := ctx.Value(executionReportKey{}).(checkreport.Contract)
	return report
}

func TaskCheckReport(t *Task) (checkreport.Contract, bool, error) {
	var records []struct {
		Kind    string
		Attempt int
		Report  checkreport.Contract
	}
	if err := json.Unmarshal([]byte(t.AttemptLog), &records); err != nil {
		return checkreport.Contract{}, false, err
	}
	for _, record := range records {
		if record.Kind == "execution_contract" && record.Attempt == t.Attempts {
			return record.Report, true, record.Report.Validate()
		}
	}
	// Historical executions predate typed stages; do not retrofit new required
	// stages onto the already-admitted attempt when configuration changes.
	for _, record := range records {
		if record.Attempt == t.Attempts {
			switch record.Kind {
			case "execution", "execution_pending", "check", "check_pending", "review", "review_pending":
				return checkreport.Contract{}, true, nil
			}
		}
	}
	return checkreport.Contract{}, false, nil
}

func freezeExecutionReport(s stateStore, t *Task, report checkreport.Contract) error {
	if err := report.Validate(); err != nil {
		return err
	}
	previous, found, err := TaskCheckReport(t)
	if err != nil {
		return err
	}
	if found {
		if !previous.Equal(report) {
			return fmt.Errorf("attempt check contract differs from current configuration; start an explicitly new attempt")
		}
		return nil
	}
	t.AttemptLog, err = outcome.AppendRecord(t.AttemptLog, struct {
		Kind    string
		Attempt int
		Report  checkreport.Contract
	}{"execution_contract", t.Attempts, report.Clone()})
	if err != nil {
		return err
	}
	return setTaskOutcome(s, t)
}
