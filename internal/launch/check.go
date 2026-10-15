package launch

import (
	"context"
	"errors"
	"fmt"

	"specific-harness/internal/agent"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// runCheck uses the same registered Bash and approved fan-out context as workers.
// Only its actual settled exit can qualify a declared command, never output prose.
func runCheck(ctx context.Context, d Deps, r *Result, t *plan.Task) (outcome.Verification, error) {
	if r.brief == nil || !t.SameBrief(r.brief) || t.Attempts != r.Attempts || t.State != r.State {
		return outcome.Unverified, fmt.Errorf("task %q changed before check admission", t.ID)
	}
	if err := plan.PausePendingRevision(plan.WithProject(ctx, d.Workspace), d.Store, t.ID, r.Attempts); err != nil {
		return outcome.Unverified, err
	}
	c := codingFromCheck(d, r, t)
	sub := roleRunner(d, d.ParentWindow)
	var recordingErr error
	sub.SessionID, sub.Session = t.ID, reviewSink{d: d, r: r, phase: "check_pending", err: &recordingErr}
	sub.Continuation.Coding = c
	sub.Workspace, sub.CheckCommand = c.Workspace, t.CheckCommand
	bash, ok := sub.Registry.Lookup("bash")
	if !ok {
		r.VerificationReason = "required check needs registered Bash; neither episode command executed"
		return outcome.Unverified, appendReview(r, attemptRecord{Kind: "check", Attempt: r.Attempts, Command: t.CheckCommand, Cwd: c.Workspace, Verification: outcome.Unverified, Error: r.VerificationReason, Outcome: outcome.Outcome{Status: outcome.Unknown, Reason: r.VerificationReason}})
	}
	def := sub.CheckDefinition(bash)
	args, repair := tool.Validate(def, map[string]string{})
	if repair != "" {
		r.VerificationReason = repair
		return outcome.Unverified, nil
	}
	_, err := childGate(d, agent.Direct)(ctx, def, args)
	v, recordErr := recordCurrentCheck(d, r, t, c)
	if ctx.Err() != nil {
		return v, errors.Join(err, recordingErr, recordErr)
	}
	return v, errors.Join(recordingErr, recordErr)
}
