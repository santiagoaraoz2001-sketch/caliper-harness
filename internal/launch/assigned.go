package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"time"
)

// Assigned captures an owning claim in memory, never from restored provenance.
type Assigned struct {
	Deps          Deps
	Root, Command string
	task          *plan.Task
	identity      *session.Assignment
	goal          string
	settled       bool
}

func (a *Assigned) AcceptanceCommand() string {
	if a.task == nil {
		return ""
	}
	return a.task.AcceptanceCommand
}

func (a *Assigned) Reset() { a.task = nil; a.identity = nil; a.settled = false }
func (a *Assigned) Start(ctx context.Context, id string) (*session.Assignment, string, error) {
	if a.task != nil && !a.settled {
		return nil, "", fmt.Errorf("finish the current assigned task before another claim")
	}
	t, err := plan.GetTask(a.Deps.Store, id)
	if err != nil {
		return nil, "", err
	}
	parent, err := plan.TaskApproach(a.Deps.Store, id)
	if err != nil {
		return nil, "", err
	}
	p, err := plan.ApproachPlan(a.Deps.Store, parent.ID)
	if err != nil {
		return nil, "", err
	}
	snapshot, err := plan.ReadinessSnapshot(a.Deps.Store, parent.ID)
	if err != nil {
		return nil, "", err
	}
	tasks, err := plan.ApproachTasks(a.Deps.Store, parent.ID)
	if err != nil {
		return nil, "", err
	}
	if err = checkCoherence(ctx, a.Deps, parent.ID, p, tasks); err != nil {
		return nil, "", err
	}
	if err = approveTaskContracts(ctx, a.Deps, t.ID); err != nil {
		return nil, "", err
	}
	t, err = plan.ClaimTaskContext(plan.WithProject(ctx, a.Root), a.Deps.Store, t.ID, snapshot, a.Command)
	if err != nil {
		return nil, "", err
	}
	a.task = t
	a.goal = p.Goal
	a.settled = false
	a.identity = &session.Assignment{Store: a.Deps.Store.Identity(), Workspace: a.Root, PlanID: p.ID, ApproachID: parent.ID, TaskID: t.ID, Attempt: t.Attempts}
	return a.identity, t.CheckCommand, nil
}
func (a *Assigned) Admit(ctx context.Context) error {
	if a.task == nil || a.settled {
		return fmt.Errorf("execution needs task_start with a real prepared same-project queued task")
	}
	if err := plan.PausePendingRevision(plan.WithProject(ctx, a.Root), a.Deps.Store, a.task.ID, a.task.Attempts); err != nil {
		return err
	}
	t, err := plan.GetTask(a.Deps.Store, a.task.ID)
	if err != nil {
		return err
	}
	parent, err := plan.TaskApproach(a.Deps.Store, t.ID)
	if err != nil {
		return err
	}
	p, err := plan.ApproachPlan(a.Deps.Store, parent.ID)
	if err != nil {
		return err
	}
	if t.State != plan.TaskRunning || t.Attempts != a.task.Attempts || !t.SameBrief(a.task) || parent.ID != a.identity.ApproachID || parent.State != plan.ApproachRunning || parent.NeedsReady || p.ID != a.identity.PlanID || p.State != plan.PlanActive || p.Goal != a.goal || p.ProjectRoot != a.Root {
		return fmt.Errorf("assigned task/brief/attempt/ancestry changed; no execution admitted")
	}
	return ctx.Err()
}
func (a *Assigned) ClaimedBrief() map[string]string { return assignedBrief(a.task) }
func (a *Assigned) Facts(ctx context.Context) (any, error) {
	if a.identity != nil && !a.settled {
		if err := a.Admit(ctx); err != nil {
			return nil, err
		}
	}
	if a.identity == nil {
		return struct{ Assignment string }{"unclaimed; project work is not Task progress"}, nil
	}
	t, err := plan.GetTask(a.Deps.Store, a.identity.TaskID)
	if err != nil {
		return nil, err
	}
	parent, err := plan.TaskApproach(a.Deps.Store, t.ID)
	if err != nil {
		return nil, err
	}
	p, err := plan.ApproachPlan(a.Deps.Store, parent.ID)
	if err != nil {
		return nil, err
	}
	return struct {
		Identity                            *session.Assignment
		TaskState, PlanState, ApproachState string
		Frozen, Current                     map[string]string
		Qualified                           bool
	}{a.identity, t.State, p.State, parent.State, assignedBrief(a.task), assignedBrief(t), t.Qualified()}, ctx.Err()
}
func (a *Assigned) Settle(ctx context.Context, status outcome.Outcome, text, request, evidence string, transcript []llm.Message, c *session.CodingState) (agent.TaskSettlement, error) {
	if a.task == nil {
		return agent.TaskSettlement{}, nil
	}
	if a.settled {
		t, e := plan.GetTask(a.Deps.Store, a.task.ID)
		if e != nil {
			return agent.TaskSettlement{}, e
		}
		return agent.TaskSettlement{TaskID: t.ID, Attempt: t.Attempts, State: t.State, Qualified: t.Qualified(), Verification: t.Verification, Feedback: t.VerificationReason}, nil
	}
	r := resultOf(a.task)
	r.State = plan.TaskRunning
	r.Output = text
	r.ExecutionRequest, r.ExecutionEvidence = request, evidence
	r.Outcome = status
	r.Transcript = transcript
	r.Skipped = false
	r.Reused = false
	if err := appendReview(&r, attemptRecord{Kind: "execution", Attempt: r.Attempts, Outcome: status, Output: text, Transcript: transcript}); err != nil {
		return agent.TaskSettlement{}, err
	}
	var err error
	check := outcome.Unverified
	if status.Status == outcome.Completed || status.Status == outcome.Failed {
		check, err = recordCurrentCheck(a.Deps, &r, a.task, c)
		if err != nil {
			return agent.TaskSettlement{}, err
		}
	}
	if status.Status == outcome.Completed {
		err = a.Admit(ctx)
	}
	if status.Status == outcome.Completed && err == nil {
		results := []Result{r}
		err = verifyPass(ctx, a.Deps, results, []int{0}, a.Deps.ParentWindow, c)
		r = results[0]
	} else {
		if err != nil {
			r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
		}
		err = errors.Join(err, settle(a.Deps.Store, &r, ctx))
	}
	a.settled = err == nil
	if a.settled {
		err = notifyTask(ctx, a.Deps, r.TaskID)
	}
	return agent.TaskSettlement{TaskID: r.TaskID, Attempt: r.Attempts, State: r.State, Check: check, Verification: r.Verification, Qualified: r.Qualified(), Feedback: r.Outcome.Reason + "\n" + r.VerificationReason + "\n" + r.Review}, err
}
func codingFromCheck(d Deps, r *Result, t *plan.Task) *session.CodingState {
	cwd := d.Workspace
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, _ = filepath.EvalSymlinks(cwd)
	c := &session.CodingState{TaskID: t.ID, Attempt: r.Attempts, Workspace: cwd, Command: t.CheckCommand, ConfigCommand: t.CheckCommand, AcceptanceCommand: t.AcceptanceCommand, Source: "configured"}
	var records []attemptRecord
	_ = json.Unmarshal([]byte(r.AttemptLog), &records)
	for i := len(records) - 1; i >= 0; i-- {
		v := records[i]
		if v.Kind == "check" {
			recorded, e := filepath.EvalSymlinks(v.Cwd)
			if e == nil && recorded == cwd && v.Attempt == r.Attempts && v.Command == t.CheckCommand && v.Command != "" && v.Started != "" && v.Ended != "" && v.Error == "" && v.Outcome.Status == outcome.Completed && v.ExitCode != nil {
				status := "incomplete"
				if v.Verification == outcome.Passed {
					status = "passed"
				} else if v.Verification == outcome.Rejected {
					status = "failed"
				}
				c.Revision = v.Revision
				c.Receipt = &session.CheckReceipt{Command: v.Command, Cwd: cwd, Revision: v.Revision, Started: v.Started, Ended: v.Ended, ExitCode: v.ExitCode, Status: status, EpisodeStarted: v.EpisodeStarted, EpisodeEnded: v.EpisodeEnded, Acceptance: v.Acceptance, Text: v.Output, Truncated: v.Truncated, Reason: v.Outcome.Reason}
			}
			break
		}
	}
	return c
}
func recordCurrentCheck(d Deps, r *Result, t *plan.Task, c *session.CodingState) (outcome.Verification, error) {
	v := outcome.Unverified
	cwd := d.Workspace
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return v, err
	}
	if c == nil || c.TaskID != t.ID || c.Attempt != r.Attempts || c.Workspace != cwd || c.Command != t.CheckCommand || c.AcceptanceCommand != t.AcceptanceCommand || c.Receipt == nil || c.Receipt.Command != t.CheckCommand || c.Receipt.Cwd != cwd || c.Receipt.Revision != c.Revision {
		return v, nil
	}
	p := c.Receipt
	if p.EpisodeStarted == "" || p.Acceptance == nil || p.Acceptance.Command != t.AcceptanceCommand || p.Acceptance.Cwd != cwd {
		return outcome.Unverified, nil
	}
	if c.Passed() {
		v = outcome.Passed
	} else if p.Status == "failed" || p.Acceptance.Status == "failed" {
		v = outcome.Rejected
	}
	if p.Status == "stale" || p.Status == "historical" {
		return outcome.Unverified, nil
	}
	record := attemptRecord{Kind: "check", Attempt: r.Attempts, Command: t.CheckCommand, Cwd: p.Cwd, Revision: p.Revision, Started: p.Started, Ended: p.Ended, EpisodeStarted: p.EpisodeStarted, EpisodeEnded: p.EpisodeEnded, Acceptance: p.Acceptance, Output: p.Text, Truncated: p.Truncated, ExitCode: p.ExitCode, Verification: v, Outcome: outcome.Outcome{Status: outcome.Unknown, Reason: p.Reason}}
	if v == outcome.Passed || v == outcome.Rejected {
		record.Outcome.Status = outcome.Completed
	}
	var records []attemptRecord
	if err = json.Unmarshal([]byte(r.AttemptLog), &records); err != nil {
		return v, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		old := records[i]
		if old.Kind == "check" {
			if old.Attempt == record.Attempt && old.Command == record.Command && old.Started == record.Started && old.Ended == record.Ended && old.EpisodeStarted == record.EpisodeStarted && old.EpisodeEnded == record.EpisodeEnded {
				return v, nil
			}
			break
		}
	}
	pending, err := popPending(r, "check_pending")
	if err != nil {
		return v, err
	}
	record.CheckEvents = pending.CheckEvents
	r.VerificationReason = fmt.Sprintf("verification episode %s; acceptance %s; configured suite %s", v, p.Acceptance.Status, p.Status)
	return v, appendReview(r, record)
}

type reviewSink struct {
	d     Deps
	r     *Result
	phase string
	err   *error
}

func (s reviewSink) Append(ctx context.Context, id string, kind, text string) (err error) {
	defer func() {
		if err != nil && s.err != nil {
			*s.err = err
		}
	}()
	if s.d.Store == nil || s.r.brief == nil || id != s.r.TaskID {
		return fmt.Errorf("role recording needs the owning Store, task and frozen brief")
	}
	if err := plan.CheckProject(s.d.Store, "task", id, s.d.Workspace); err != nil {
		return err
	}

	var records []json.RawMessage
	if err := json.Unmarshal([]byte(s.r.AttemptLog), &records); err != nil {
		return err
	}
	recordKind := s.phase
	if recordKind == "" {
		recordKind = "review_pending"
	}
	index := -1
	var record attemptRecord
	// Owning comment delivery can append after our transient role record.
	// Keep adding events to the same kind/attempt, preserving intervening raw
	// records rather than starting a second fragment that finalization loses.
	for i := len(records) - 1; i >= 0; i-- {
		var candidate attemptRecord
		if err := json.Unmarshal(records[i], &candidate); err != nil {
			return err
		}
		if candidate.Kind == recordKind && candidate.Attempt == s.r.Attempts {
			index, record = i, candidate
			break
		}
	}
	if index < 0 {
		record = attemptRecord{Kind: recordKind, Attempt: s.r.Attempts, Outcome: outcome.Outcome{Status: outcome.Unknown}}
		records = append(records, nil)
		index = len(records) - 1
	}
	event := session.Event{Time: time.Now().UTC(), Kind: kind, Text: text}
	if s.phase == "check_pending" {
		record.CheckEvents = append(record.CheckEvents, event)
	} else if s.phase == "execution_pending" {
		record.ExecutionEvents = append(record.ExecutionEvents, event)
	} else {
		record.ReviewerEvents = append(record.ReviewerEvents, event)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	records[index] = raw
	log := "[]"
	for _, raw := range records {
		log, err = outcome.AppendRecord(log, raw)
		if err != nil {
			return err
		}
	}
	s.r.AttemptLog = log
	t, err := plan.GetTask(s.d.Store, s.r.TaskID)
	if err != nil {
		return err
	}
	t.AttemptLog = s.r.AttemptLog
	expected := *s.r.brief
	expected.State = s.r.State
	expected.Attempts = s.r.Attempts
	if err := plan.SettleTaskOutcome(s.d.Store, t, s.r.State, &expected); err != nil {
		return err
	}
	s.r.AttemptLog = t.AttemptLog // Includes concurrently delivered raw comment records.
	return nil
}
func runReviewer(ctx context.Context, d Deps, prompt string, window int, r *Result, t *plan.Task, c *session.CodingState) (string, []llm.Message, outcome.Outcome, error) {
	current, err := plan.GetTask(d.Store, t.ID)
	if err != nil {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, err
	}
	comments, err := deliveredReviewComments(current, r)
	if err != nil {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, err
	}
	sub := roleRunner(d, window)
	sub.SessionID, sub.Session = t.ID, reviewSink{d: d, r: r}
	sub.Continuation = session.Continuation{Coding: c}
	bindRoleComments(sub, d, t)
	for _, comment := range comments {
		raw, err := json.Marshal(comment)
		if err != nil {
			return "", nil, outcome.Outcome{Status: outcome.Failed}, err
		}
		sub.Continuation.Add("note", "user", d.Prompts.Text("continuation.task_comment_prefix")+string(raw), false)
	}
	sub.Workspace, sub.CheckCommand = c.Workspace, t.CheckCommand
	bash, ok := sub.Registry.Lookup("bash")
	if !ok {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, fmt.Errorf("reviewer needs registered Bash")
	}
	check := sub.CheckDefinition(bash)
	sub.Registry.Register(check)
	sub.Registry.Restrict([]string{"read", "read_outside", "find_outside", "grep", "find_file", "find_dir", "help", "bash", "check"})
	sub.Reviewing = true
	sub.TaskSystem = d.Prompts.Format("agent.reviewer_role", t.Title, t.Prompt, t.Target, t.Acceptance)
	sub.FinalFeedback = func(text string) string {
		switch reviewToken(text) {
		case "PASS", "FAIL", "UNVERIFIED":
			return ""
		}
		return d.Prompts.Text("continuation.review_verdict")
	}
	var roleViolation error
	effects := sub.CodingExec(agent.Direct)
	sub.Exec = childGate(d, func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
		if err := plan.PausePendingRevision(plan.WithProject(ctx, d.Workspace), d.Store, t.ID, r.Attempts); err != nil {
			return tool.Result{}, err
		}
		if def.Name != "bash" && def.Name != "check" {
			return effects(ctx, def, args)
		}
		before, beforeErr := sub.WorkspaceFingerprint(ctx)
		res, execErr := agent.Direct(ctx, def, args)
		after, afterErr := sub.WorkspaceFingerprint(ctx)
		if beforeErr != nil || afterErr != nil || before != after {
			if beforeErr != nil || afterErr != nil {
				roleViolation = fmt.Errorf("reviewer project-content observation unavailable: before=%v after=%v", beforeErr, afterErr)
			} else {
				roleViolation = fmt.Errorf("reviewer shell modified project contents; return findings instead of modifying the task")
			}
			sub.Continuation.Coding.Invalidate(roleViolation.Error())
			res.Failed = true
			execErr = errors.Join(execErr, roleViolation, sub.Session.Append(context.WithoutCancel(ctx), t.ID, "role_violation", roleViolation.Error()))
		}
		return res, execErr
	})
	out, messages, err := sub.Run(ctx, prompt)
	if roleViolation != nil {
		sub.Continuation.Coding.Invalidate(roleViolation.Error())
		if sub.Outcome.Status != outcome.Cancelled && sub.Outcome.Status != outcome.Stopped {
			sub.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: roleViolation.Error()}
		}
		err = errors.Join(err, roleViolation)
	}
	if sub.Continuation.Coding != nil {
		*c = *sub.Continuation.Coding
	}
	return out, messages, sub.Outcome, err
}

// Workers and reviewers receive current-attempt guidance through the same
// owning polling/delivery seam. Comments cannot change their frozen brief.
func bindRoleComments(sub *agent.Runner, d Deps, t *plan.Task) {
	sub.TaskComments = func(ctx context.Context) ([]outcome.TaskComment, error) {
		actual, err := plan.GetTask(d.Store, t.ID)
		if err != nil {
			return nil, err
		}
		if actual.State != t.State || actual.Attempts != t.Attempts || !actual.SameBrief(t) {
			return nil, fmt.Errorf("task changed before comment delivery")
		}
		return outcome.TaskComments(actual.AttemptLog, t.Attempts)
	}
	sub.CommentReceipt = func(ctx context.Context, comment outcome.TaskComment) error {
		return plan.RecordCommentDelivery(plan.WithProject(ctx, d.Workspace), d.Store, comment, t.ID)
	}
}

// Current-attempt advice requires the same owning delivery receipts for direct
// main execution and workers. Model/tool transcript text cannot prove delivery.
func deliveredReviewComments(t *plan.Task, r *Result) ([]outcome.TaskComment, error) {
	comments, err := outcome.TaskComments(t.AttemptLog, r.Attempts)
	if err != nil {
		return nil, err
	}
	if len(comments) == 0 {
		return nil, nil
	}
	var records []struct {
		Kind, CommentID string
		Attempt         int
	}
	if err := json.Unmarshal([]byte(t.AttemptLog), &records); err != nil {
		return nil, err
	}
	delivered := map[string]bool{}
	for _, record := range records {
		if record.Kind == "comment_delivery" && record.Attempt == r.Attempts {
			delivered[record.CommentID] = true
		}
	}
	selected := []outcome.TaskComment{}
	for _, comment := range comments {
		if comment.TaskID != t.ID {
			continue
		}
		if delivered[comment.ID] {
			selected = append(selected, comment)
		}
	}
	return selected, nil
}

func parentRequirements(d Deps, t *plan.Task) (string, string, error) {
	if d.Store == nil || t == nil {
		return "", "", fmt.Errorf("role requirements need the owning Store and task")
	}
	if err := plan.CheckProject(d.Store, "task", t.ID, d.Workspace); err != nil {
		return "", "", err
	}
	approach, err := plan.TaskApproach(d.Store, t.ID)
	if err != nil {
		return "", "", err
	}
	parent, err := plan.ApproachPlan(d.Store, approach.ID)
	if err != nil {
		return "", "", err
	}
	return parent.Goal, approach.Title, nil
}

func assignedBrief(t *plan.Task) map[string]string {
	return map[string]string{"title": t.Title, "prompt": t.Prompt, "current": t.Current, "target": t.Target, "sketch": t.Sketch, "acceptance": t.Acceptance, "tests": t.Tests, "verify": t.Verify, "check_command": t.CheckCommand, "acceptance_command": t.AcceptanceCommand}
}

func (a *Assigned) Comments(ctx context.Context) ([]outcome.TaskComment, error) {
	if a.task == nil || a.settled {
		return nil, nil
	}
	if err := a.Admit(ctx); err != nil {
		return nil, err
	}
	t, err := plan.GetTask(a.Deps.Store, a.task.ID)
	if err != nil {
		return nil, err
	}
	return outcome.TaskComments(t.AttemptLog, a.task.Attempts)
}
func (a *Assigned) AppendComment(ctx context.Context, text, sessionRef string) error {
	if err := a.Admit(ctx); err != nil {
		return err
	}
	_, err := plan.AppendTaskComment(plan.WithProject(ctx, a.Root), a.Deps.Store, a.task.ID, a.task.Attempts, "user", sessionRef, text)
	return err
}
