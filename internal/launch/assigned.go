package launch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/pathutil"
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
	contract      *plan.ExecutionContract
	settled       bool
}

func (a *Assigned) AcceptanceCommand() string {
	if a.task == nil {
		return ""
	}
	return a.task.AcceptanceCommand
}

func (a *Assigned) Reset() { a.task = nil; a.identity = nil; a.contract = nil; a.settled = false }
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
	t, err = plan.ClaimTaskContext(plan.WithExecutionReport(plan.WithProject(ctx, a.Root), a.Deps.CheckReport), a.Deps.Store, t.ID, snapshot, a.Command)
	if err != nil {
		return nil, "", err
	}
	a.task = t
	a.contract = &plan.ExecutionContract{Task: t, Report: a.Deps.CheckReport.Clone(), PlanID: p.ID, ApproachID: parent.ID, ProjectRoot: a.Root, Goal: p.Goal, Strategy: parent.Title}
	a.settled = false
	a.identity = &session.Assignment{Store: a.Deps.Store.Identity(), Workspace: a.Root, PlanID: p.ID, ApproachID: parent.ID, TaskID: t.ID, Attempt: t.Attempts}
	return a.identity, t.CheckCommand, nil
}
func (a *Assigned) Admit(ctx context.Context) error {
	if a.task == nil || a.settled {
		return fmt.Errorf("execution needs task_start with a real prepared same-project queued task")
	}
	return a.contract.Admit(plan.WithProject(ctx, a.Root), a.Deps.Store)
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
	frozen, current := assignedBrief(a.task), assignedBrief(t)
	matched, basis := maps.Equal(frozen, current), ""
	if a.settled && t.Qualified() && t.Attempts == a.identity.Attempt && t.SameBrief(a.task) {
		raw, _ := json.Marshal(frozen)
		basis = fmt.Sprintf("settled qualified attempt; authored brief omitted: %d bytes, sha256=%x", len(raw), sha256.Sum256(raw))
		frozen, current, matched = nil, nil, false
	} else if matched {
		current = nil
	}
	return struct {
		Identity                            *session.Assignment
		TaskState, PlanState, ApproachState string
		Title, Target                       string
		Frozen, Current                     map[string]string `json:",omitempty"`
		CurrentMatchesFrozen                bool              `json:",omitempty"`
		BriefBasis                          string            `json:",omitempty"`
		Qualified                           bool
	}{a.identity, t.State, p.State, parent.State, t.Title, t.Target, frozen, current, matched, basis, t.Qualified()}, ctx.Err()
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
	cwd, _ = pathutil.EvalSymlinks(cwd)
	c := &session.CodingState{TaskID: t.ID, Attempt: r.Attempts, Workspace: cwd, Command: t.CheckCommand, ConfigCommand: t.CheckCommand, AcceptanceCommand: t.AcceptanceCommand, Source: "configured", Report: d.CheckReport.Clone()}
	if report, found, err := plan.TaskCheckReport(t); err == nil && found {
		c.Report = report.Clone()
	}
	var records []attemptRecord
	_ = json.Unmarshal([]byte(r.AttemptLog), &records)
	for i := len(records) - 1; i >= 0; i-- {
		v := records[i]
		if v.Kind == "check" {
			recorded, e := pathutil.EvalSymlinks(v.Cwd)
			if e == nil && recorded == cwd && v.Attempt == r.Attempts && v.Command == t.CheckCommand && v.Command != "" && v.Started != "" && v.Ended != "" && v.Error == "" && v.Outcome.Status == outcome.Completed && v.ExitCode != nil && v.Report.Equal(c.Report) {
				status := "incomplete"
				if v.Verification == outcome.Passed || v.Verification == outcome.Rejected {
					status = "failed"
					if *v.ExitCode == 0 {
						status = "passed"
					}
				}
				c.Revision = v.Revision
				c.Receipt = &session.CheckReceipt{Command: v.Command, Cwd: cwd, Revision: v.Revision, Started: v.Started, Ended: v.Ended, ExitCode: v.ExitCode, Status: status, EpisodeStarted: v.EpisodeStarted, EpisodeEnded: v.EpisodeEnded, Acceptance: v.Acceptance, Text: v.Output, Truncated: v.Truncated, Reason: v.Outcome.Reason, Report: v.Report.Clone(), RunID: v.RunID, Coverage: v.Coverage, Prerequisites: v.Prerequisites}
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
	cwd, err := pathutil.EvalSymlinks(cwd)
	if err != nil {
		return v, err
	}
	if c == nil || c.TaskID != t.ID || c.Attempt != r.Attempts || c.Workspace != cwd || c.Command != t.CheckCommand || c.AcceptanceCommand != t.AcceptanceCommand || !c.Report.Equal(d.CheckReport) || c.Receipt == nil || c.Receipt.Command != t.CheckCommand || c.Receipt.Cwd != cwd || c.Receipt.Revision != c.Revision {
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
	record := attemptRecord{Kind: "check", Attempt: r.Attempts, Command: t.CheckCommand, Cwd: p.Cwd, Revision: p.Revision, Started: p.Started, Ended: p.Ended, EpisodeStarted: p.EpisodeStarted, EpisodeEnded: p.EpisodeEnded, Acceptance: p.Acceptance, Output: p.Text, Truncated: p.Truncated, ExitCode: p.ExitCode, Verification: v, Outcome: outcome.Outcome{Status: outcome.Unknown, Reason: p.Reason}, Report: p.Report.Clone(), RunID: p.RunID, Coverage: p.Coverage, Prerequisites: p.Prerequisites}
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
	record.CheckSession = pending.CheckSession
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
	if s.d.SessionsDir != "" {
		// Keep append-only raw evidence in the existing session archive. The
		// owning graph stores a phase reference once, not a growing full log
		// on every token/tool event. Legacy callers keep inline evidence.
		current, err := plan.GetTask(s.d.Store, id)
		if err != nil {
			return err
		}
		if current.State != s.r.State || current.Attempts != s.r.Attempts || !current.SameBrief(s.r.brief) {
			return fmt.Errorf("role evidence owner changed")
		}
		ref := &record.ReviewerSession
		if s.phase == "check_pending" {
			ref = &record.CheckSession
		} else if s.phase == "execution_pending" {
			ref = &record.ExecutionSession
		}
		persistRef := *ref == ""
		if persistRef {
			*ref = session.NewID()
			if err := session.Append(s.d.SessionsDir, *ref, session.Event{Kind: "workspace", Text: s.d.Workspace}); err != nil {
				return err
			}
			owner, _ := json.Marshal(struct {
				Task    string
				Attempt int
				Phase   string
			}{id, s.r.Attempts, recordKind})
			if err := session.Append(s.d.SessionsDir, *ref, session.Event{Kind: "role_owner", Text: string(owner)}); err != nil {
				return err
			}
		}
		if err := session.Append(s.d.SessionsDir, *ref, event); err != nil {
			return err
		}
		if !persistRef {
			return nil
		}
	} else if s.phase == "check_pending" {
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
	parentCtx := ctx
	ctx, stopReview := context.WithCancelCause(ctx)
	defer stopReview(nil)
	current, err := plan.GetTask(d.Store, t.ID)
	if err != nil {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, err
	}
	if current.State != r.State || current.Attempts != r.Attempts || !current.SameBrief(r.brief) {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, fmt.Errorf("reviewer attempt or brief changed")
	}
	owner, err := plan.CaptureReviewExecution(plan.WithExecutionReport(plan.WithProject(ctx, d.Workspace), d.CheckReport), d.Store, current)
	if err != nil {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, err
	}
	comments, err := deliveredReviewComments(current, r)
	if err != nil {
		return "", nil, outcome.Outcome{Status: outcome.Failed}, err
	}
	sub := roleRunner(d, window)
	sub.CheckReport = owner.Report.Clone()
	bindProjectContext(sub, d, owner)
	sub.SessionID, sub.Session = t.ID, reviewSink{d: d, r: r}
	if d.NewKnowledge != nil {
		knowledge, err := d.NewKnowledge(t.ID, true)
		if err != nil {
			return "", nil, outcome.Outcome{Status: outcome.Failed}, err
		}
		sub.Registry.Register(knowledge)
	}
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
	// The reviewer can inspect sources and run only the already frozen check
	// episode. It cannot invent free-form shell probes or scratch writes.
	sub.Registry.Restrict([]string{"read", "read_outside", "find_outside", "grep", "find_file", "find_dir", "help", "check", "graphql"})
	sub.Reviewing = true
	if d.ConfigureReviewer != nil {
		d.ConfigureReviewer(sub)
	}
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
		if err := owner.Admit(plan.WithProject(ctx, d.Workspace), d.Store); err != nil {
			return tool.Result{}, err
		}
		if def.Name != "check" {
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
			// This role can no longer qualify. Stop before another model request;
			// cleanup or additional probes cannot undo the observed violation.
			stopReview(roleViolation)
		}
		return res, execErr
	})
	out, messages, err := sub.Run(ctx, prompt)
	if roleViolation != nil {
		sub.Continuation.Coding.Invalidate(roleViolation.Error())
		if parentCtx.Err() == nil {
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
func (a *Assigned) AppendComment(ctx context.Context, text, sessionRef string, noteIDs ...string) error {
	if a.task == nil || a.identity == nil || a.identity.TaskID != a.task.ID || a.identity.Attempt != a.task.Attempts || a.identity.Store != a.Deps.Store.Identity() || a.identity.Workspace != a.Root {
		return fmt.Errorf("user feedback needs the captured task/project/attempt identity")
	}
	parent, err := plan.TaskApproach(a.Deps.Store, a.task.ID)
	if err != nil {
		return err
	}
	p, err := plan.ApproachPlan(a.Deps.Store, parent.ID)
	if err != nil {
		return err
	}
	if parent.ID != a.identity.ApproachID || p.ID != a.identity.PlanID {
		return fmt.Errorf("user feedback owner ancestry changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = plan.AppendUserTaskFeedback(plan.WithProject(ctx, a.Root), a.Deps.Store, a.task.ID, a.task.Attempts, sessionRef, text, noteIDs...)
	return err
}
