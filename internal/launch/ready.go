package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/auth"
	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
)

// ReadyCheck judges whether a ready approach may fan out. Mechanical
// first (ready state, drafted tasks via the same ValidateApproachReady
// the ready transition uses), then the decision model judges coherence
// against the plan goal; unsure escalates to ask. Reject leaves the
// approach untouched — nothing transitions here.
func ReadyCheck(ctx context.Context, d Deps, approachID string) error {
	_, err := CheckReady(ctx, d, approachID)
	return err
}

// CheckReady binds judgment to the exact brief that LaunchChecked admits.
func CheckReady(ctx context.Context, d Deps, approachID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	snapshot, err := plan.ReadinessSnapshot(d.Store, approachID)
	if err != nil {
		return "", err
	}
	if err := readyCheck(ctx, d, approachID); err != nil {
		return "", err
	}
	current, err := plan.ReadinessSnapshot(d.Store, approachID)
	if err != nil {
		return "", err
	}
	if current != snapshot {
		return "", fmt.Errorf("approach %q changed during readiness — check it again", approachID)
	}
	return snapshot, nil
}
func readyCheck(ctx context.Context, d Deps, approachID string) error {
	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return err
	}
	if a.State != plan.ApproachReady {
		return fmt.Errorf("approach %q is %s — launch needs ready", approachID, a.State)
	}
	if err := plan.ValidateApproachReady(d.Store, approachID); err != nil {
		return err
	}
	p, err := plan.ApproachPlan(d.Store, approachID)
	if err != nil {
		return err
	}
	tasks, err := plan.ApproachTasks(d.Store, approachID)
	if err != nil {
		return err
	}
	return checkCoherence(ctx, d, approachID, p, tasks)
}

// Ready uses the same complete coherence gate before the owning transition.
func Ready(ctx context.Context, d Deps, approachID string) (*plan.Approach, error) {
	return plan.Ready(ctx, d.Store, approachID, func(ctx context.Context, p *plan.Plan, tasks []*plan.Task) error {
		return checkCoherence(ctx, d, approachID, p, tasks)
	})
}
func checkCoherence(ctx context.Context, d Deps, approachID string, p *plan.Plan, tasks []*plan.Task) error {
	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return err
	}
	if err := requiredCommands(d, tasks); err != nil {
		return err
	}
	req, size, err := coherenceRequest(d, p, a, tasks)
	if err != nil {
		return err
	}
	if d.ManualReadiness {
		prompt := fmt.Sprintf("Decision model disabled — approve approach %q as ready for execution? Review the complete task briefs below; this is readiness approval, not evidence that code passed checks or independent review.\n", approachID)
		prompt += humanReadinessContext(p, a, tasks, req)
		accepted := false
		var confirmedSnapshot string
		if d.ConfirmReadiness != nil {
			snapshot, err := plan.ReadinessBriefSnapshot(d.Store, approachID, p, a, tasks)
			if err != nil {
				return err
			}
			accepted, err = d.ConfirmReadiness(ctx, approachID, snapshot, req.Context, prompt)
			if err != nil {
				return err
			}
			confirmedSnapshot = snapshot
		} else if d.Ask != nil {
			accepted = d.Ask(ctx, prompt)
		} else if d.In != nil && d.Out != nil {
			accepted = auth.Ask(ctx, d.In, d.Out, prompt)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Log != nil {
			raw, _ := json.Marshal(struct {
				Approach, Brief string
				Approved        bool
			}{approachID, req.Context, accepted})
			d.Log("manual-readiness", string(raw))
		}
		if !accepted {
			if confirmedSnapshot != "" {
				return &outcome.ReadinessNotApprovedError{Store: d.Store.Identity(), ApproachID: approachID, Snapshot: confirmedSnapshot}
			}
			return fmt.Errorf("readiness not approved; approve the exact brief in an interactive session or enable the decision model")
		}
		return nil
	}
	if d.Judge == nil {
		return fmt.Errorf("PAT readiness has no decision judge; enable it in Settings (decision.disabled: false) — refusing %q", approachID)
	}
	_ = size // complete encoded size is diagnostic only; no byte ceiling refuses admission
	v, err := d.Judge(ctx, req)
	if err != nil && decision.IsCapacity(err) && ctx.Err() == nil {
		v, err = judgeCoherenceChunked(ctx, d, p, a, tasks, req)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if decision.EncodedCapacity(err) {
			return fmt.Errorf("launch refused: %v — %s", err, d.Prompts.Text("guidance.judge.capacity"))
		}
		return fmt.Errorf("launch refused: the decision sidecar refused or is unreachable (%v) — %s", err, d.Prompts.Text("guidance.judge.unreachable"))
	}
	if v.Option != req.Options[0] && v.Option != req.Options[1] {
		return fmt.Errorf("launch refused: decision returned unknown readiness option %q", v.Option)
	}
	if v.Confidence < d.Threshold {
		prompt := fmt.Sprintf("unsure — leans %s (confidence %.3f) — admit approach %q anyway?", v.Option, v.Confidence, approachID)
		prompt += "\nComplete judged brief:\n" + humanReadinessContext(p, a, tasks, req)
		accepted := false
		var confirmedSnapshot string
		if d.ConfirmReadiness != nil {
			snapshot, err := plan.ReadinessBriefSnapshot(d.Store, approachID, p, a, tasks)
			if err != nil {
				return err
			}
			accepted, err = d.ConfirmReadiness(ctx, approachID, snapshot, req.Context, prompt)
			if err != nil {
				return err
			}
			confirmedSnapshot = snapshot
		} else if d.Ask != nil {
			accepted = d.Ask(ctx, prompt)
		} else if d.In != nil && d.Out != nil {
			accepted = auth.Ask(ctx, d.In, d.Out, prompt)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !accepted {
			decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "asked-refused", d.JudgmentContextChars)
			if confirmedSnapshot != "" {
				return &outcome.ReadinessNotApprovedError{Store: d.Store.Identity(), ApproachID: approachID, Snapshot: confirmedSnapshot}
			}
			return fmt.Errorf("launch refused: coherence unsure (confidence %.2f)", v.Confidence)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "asked-admitted", d.JudgmentContextChars)
		return nil
	}
	if v.Option == req.Options[1] {
		decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "refused", d.JudgmentContextChars)
		return fmt.Errorf("launch refused: the decision model judged approach %q incoherent or insufficiently bounded for goal %q (confidence %.2f)", approachID, p.Goal, v.Confidence)
	}
	decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "admitted", d.JudgmentContextChars)
	return nil
}

// ReadinessBudget reports the current complete request, without judging or transitioning.
// Draft work may exceed the budget; Ready rebuilds and enforces it independently.
func ReadinessBudget(d Deps, approachID string) (size, maximum, overage int, err error) {
	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return 0, 0, 0, err
	}
	p, err := plan.ApproachPlan(d.Store, approachID)
	if err != nil {
		return 0, 0, 0, err
	}
	tasks, err := plan.ApproachTasks(d.Store, approachID)
	if err != nil {
		return 0, 0, 0, err
	}
	_, size, err = coherenceRequest(d, p, a, tasks)
	maximum, overage = 0, 0 // no byte ceiling; provider token admission is the only limit
	return
}

func coherenceRequest(d Deps, p *plan.Plan, a *plan.Approach, tasks []*plan.Task) (decision.Request, int, error) {
	req := decision.Request{
		Context:      coherenceContext(p, a, tasks),
		Instructions: d.Prompts.Text("decision.readiness.instructions"),
		Options:      []string{d.Prompts.Text("decision.readiness.ready"), d.Prompts.Text("decision.readiness.revise")},
	}
	if d.ReadinessRequirements != nil {
		if requirements := d.ReadinessRequirements(); requirements != "" {
			req.Context += "\nCurrent delivered user requirements (reconcile these with the owning briefs):\n" + requirements
		}
	}
	if strings.TrimSpace(req.Options[0]) == "" || strings.TrimSpace(req.Options[1]) == "" || req.Options[0] == req.Options[1] {
		return req, 0, fmt.Errorf("readiness decision options must be nonblank and distinct")
	}
	raw, err := json.Marshal(req)
	return req, len(raw), err
}

// coherenceContext retains every task field. Size is handled by chunked
// judgments when the complete request exceeds the provider limit, never
// by dropping authored fields from judgment.
func coherenceContext(p *plan.Plan, a *plan.Approach, tasks []*plan.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan goal: %s\nApproach %s declared scope and strategy: %s\nTasks (assess their collective coverage of that Approach):\n", p.Goal, a.ID, a.Title)
	for i, t := range tasks {
		fmt.Fprintf(&b, "%d. %s — %s\ncurrent: %s\ntarget: %s\nsketch: %s\nacceptance: %s\ntests: %s\nverify: %s\nacceptance_command: %s\ncheck_command: %s\n", i+1, t.Title, t.Prompt, t.Current, t.Target, t.Sketch, t.Acceptance, t.Tests, t.Verify, t.AcceptanceCommand, t.CheckCommand)
	}
	return b.String()
}

// judgeCoherenceChunked judges a brief whose complete request exceeds the
// provider limit: one bounded verdict per task, then one collective verdict
// over compact task lines. The host aggregates deterministically: any revise
// verdict refuses, confidence is the minimum. No evidence is truncated.
func judgeCoherenceChunked(ctx context.Context, d Deps, p *plan.Plan, a *plan.Approach, tasks []*plan.Task, collective decision.Request) (decision.Verdict, error) {
	taskOptions := []string{d.Prompts.Text("decision.readiness_task.ready"), d.Prompts.Text("decision.readiness_task.revise")}
	verdicts := make([]decision.Verdict, 0, len(tasks)+1)
	for _, t := range tasks {
		req := decision.Request{
			Context:      coherenceContext(p, a, []*plan.Task{t}),
			Instructions: d.Prompts.Text("decision.readiness_task.instructions"),
			Options:      taskOptions,
		}
		v, err := d.Judge(ctx, req)
		if err != nil {
			if decision.IsCapacity(err) {
				return decision.Verdict{}, fmt.Errorf("task %q alone exceeds the decision provider limit — shorten its acceptance or split the task: %w", t.Title, err)
			}
			return decision.Verdict{}, err
		}
		decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "chunked-task", d.JudgmentContextChars)
		verdicts = append(verdicts, v)
		if v.Option == taskOptions[1] {
			return decision.Verdict{Pick: 1, Option: collective.Options[1], Confidence: v.Confidence, Model: v.Model, Device: v.Device, Protocol: v.Protocol, ConfidenceSource: v.ConfidenceSource}, nil
		}
	}
	var lines strings.Builder
	fmt.Fprintf(&lines, "Plan goal: %s\nApproach %s declared scope and strategy: %s\nTasks:\n", p.Goal, a.ID, a.Title)
	for i, t := range tasks {
		fmt.Fprintf(&lines, "%d. %s — %s\ntarget: %s\n", i+1, t.Title, t.Prompt, t.Target)
	}
	req := decision.Request{Context: lines.String(), Instructions: collective.Instructions, Options: collective.Options}
	v, err := d.Judge(ctx, req)
	if err != nil {
		return decision.Verdict{}, err
	}
	decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "chunked-collective", d.JudgmentContextChars)
	verdicts = append(verdicts, v)
	min := verdicts[0].Confidence
	for _, x := range verdicts[1:] {
		if x.Confidence < min {
			min = x.Confidence
		}
	}
	v.Confidence = min
	return v, nil
}

// Presentation is separate from the exact snapshot and judge input. Every field
// remains visible, including unknown structured fields from older/newer clients.
func humanReadinessContext(p *plan.Plan, a *plan.Approach, tasks []*plan.Task, req decision.Request) string {
	copyPlan := *p
	copyPlan.Goal = plan.DisplayStructured(p.Goal)
	copyApproach := *a
	copyApproach.Title = plan.DisplayStructured(a.Title)
	copyTasks := make([]*plan.Task, 0, len(tasks))
	for _, task := range tasks {
		copyTask := *task
		copyTask.Sketch = plan.DisplayStructured(task.Sketch)
		copyTasks = append(copyTasks, &copyTask)
	}
	// Retain the exact requirements captured for this judgment; do not call the
	// live delivery accessor again while presenting an unsure/manual decision.
	suffix := strings.TrimPrefix(req.Context, coherenceContext(p, a, tasks))
	return plan.DisplayText(coherenceContext(&copyPlan, &copyApproach, copyTasks) + suffix)
}

// Required execution needs a declared command before admitting queued work.
// Settled historical tasks retain their original policy and reviewer-only path.
func requiredCommands(d Deps, tasks []*plan.Task) error {
	for _, t := range tasks {
		if d.Wf.VerifyFollowup && t.State == plan.TaskQueued && (strings.TrimSpace(t.CheckCommand) == "" || strings.TrimSpace(t.AcceptanceCommand) == "") {
			return fmt.Errorf("task %q needs check_command and acceptance_command before ready/launch — use graphql mutation setTaskBlock(id,current,target,sketch,acceptance,tests,verify,checkCommand,acceptanceCommand) with supported fields and an explicit command, or workflow_block when advertised", t.ID)
		}
	}
	return nil
}
func admissionCommands(d Deps, id string) error {
	tasks, err := plan.ApproachTasks(d.Store, id)
	if err != nil {
		return err
	}
	return requiredCommands(d, tasks)
}
