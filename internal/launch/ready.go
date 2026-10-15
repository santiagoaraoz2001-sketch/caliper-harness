package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/auth"
	"specific-harness/internal/decision"
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
	if err := requiredCommands(d, tasks); err != nil {
		return err
	}
	if d.Judge == nil {
		return fmt.Errorf("launch gate has no decision judge wired — refusing %q", approachID)
	}
	req, size, err := coherenceRequest(d, p, tasks)
	if err != nil {
		return err
	}
	if d.CoherenceMaxChars > 0 && size > d.CoherenceMaxChars {
		return fmt.Errorf("readiness request is %d bytes, exceeds decision.coherence_max_chars=%d — split or narrow the full brief; no judgment ran", size, d.CoherenceMaxChars)
	}
	v, err := d.Judge(ctx, req)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("launch refused: the decision sidecar refused or is unreachable (%v) — start it (python3 python/decision/server.py --port 8090)", err)
	}
	if v.Option != req.Options[0] && v.Option != req.Options[1] {
		return fmt.Errorf("launch refused: decision returned unknown readiness option %q", v.Option)
	}
	if v.Confidence < d.Threshold {
		prompt := fmt.Sprintf("unsure — leans %s (confidence %.3f) — admit approach %q anyway?", v.Option, v.Confidence, approachID)
		prompt += "\nJudged context (quoted rows; escapes are literal characters):"
		for _, row := range strings.Split(req.Context, "\n") {
			prompt += fmt.Sprintf("\n%+q", row)
		}
		accepted := false
		if d.Ask != nil {
			accepted = d.Ask(ctx, prompt)
		} else if d.In != nil && d.Out != nil {
			accepted = auth.Ask(ctx, d.In, d.Out, prompt)
		}
		if !accepted {
			decision.LogJudgment(d.Log, decision.SourceCoherence, req, v, "asked-refused", d.JudgmentContextChars)
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
	p, err := plan.ApproachPlan(d.Store, approachID)
	if err != nil {
		return 0, 0, 0, err
	}
	tasks, err := plan.ApproachTasks(d.Store, approachID)
	if err != nil {
		return 0, 0, 0, err
	}
	_, size, err = coherenceRequest(d, p, tasks)
	maximum = d.CoherenceMaxChars
	if maximum > 0 && size > maximum {
		overage = size - maximum
	}
	return
}

func coherenceRequest(d Deps, p *plan.Plan, tasks []*plan.Task) (decision.Request, int, error) {
	req := decision.Request{
		Context:      coherenceContext(p, tasks),
		Instructions: d.Prompts.Text("decision.readiness.instructions"),
		Options:      []string{d.Prompts.Text("decision.readiness.ready"), d.Prompts.Text("decision.readiness.revise")},
	}
	if strings.TrimSpace(req.Options[0]) == "" || strings.TrimSpace(req.Options[1]) == "" || req.Options[0] == req.Options[1] {
		return req, 0, fmt.Errorf("readiness decision options must be nonblank and distinct")
	}
	raw, err := json.Marshal(req)
	return req, len(raw), err
}

// coherenceContext retains every task field; the complete request is bounded above.
func coherenceContext(p *plan.Plan, tasks []*plan.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan goal: %s\nTasks:\n", p.Goal)
	for i, t := range tasks {
		fmt.Fprintf(&b, "%d. %s — %s\ncurrent: %s\ntarget: %s\nsketch: %s\nacceptance: %s\ntests: %s\nverify: %s\nacceptance_command: %s\ncheck_command: %s\n", i+1, t.Title, t.Prompt, t.Current, t.Target, t.Sketch, t.Acceptance, t.Tests, t.Verify, t.AcceptanceCommand, t.CheckCommand)
	}
	return b.String()
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
