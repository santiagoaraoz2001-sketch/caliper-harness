// Package launch fans out one approach's tasks to subagent Runners:
// sequential or concurrent per config, each task retried per workflow
// policy, results collected in task order with full outputs stored on
// their task nodes.
package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"specific-harness/internal/prompts"
	"strings"
	"sync"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/hw"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// Deps wires a launch: state, factories, steps, templates, parent window,
// plus the execution and retry policies.
type Deps struct {
	Prompts       *prompts.Catalog
	Behavior      string // loaded/capped once per builder, shared by workers and reviewers
	BehaviorNote  string // caller-visible truncation note; not model instructions
	Workspace     string // captured physical cwd for Bash and check receipts
	Store         *store.Store
	NewModel      func() agent.Model
	NewRegistry   func() *tool.Registry
	MaxSteps      int
	MaxRunSeconds int
	TemplatesDir  string
	// ParentWindow is the resolved parent window; subagent window 0 inherits it.
	ParentWindow int
	Sub          config.SubagentsConfig
	Wf           config.WorkflowsConfig
	// Judge and Threshold drive the launch ready-check; In/Out carry
	// its unsure-prompt. A nil Judge refuses every launch.
	Judge     func(context.Context, decision.Request) (decision.Verdict, error)
	Threshold float64
	In        io.Reader
	Out       io.Writer
	// CoherenceMaxChars caps the ready-check question; LoopRepeat and
	// LoopStall arm subagent loop detection (0 disables each).
	CoherenceMaxChars int
	LoopRepeat        int
	LoopStall         int
	ThinkingLoop      agent.ThinkingLoop
	// Log takes one judgment event per coherence verdict (nil skips);
	// JudgmentContextChars caps its context (0 = whole).
	Log                  func(kind, text string)
	JudgmentContextChars int
	// Compaction arms subagent loop-context compaction (Trigger 0 off).
	Compaction agent.Compaction
	// Prune arms subagent output/schema stubbing (zero off).
	Prune agent.Prune
	// AuthMode gates model-initiated fan-out (see approveLaunch).
	// Approve asks the user; nil withholds approval headlessly.
	AuthMode        string
	Approve         func(ctx context.Context, prompt string) bool
	Ask             func(ctx context.Context, prompt string) bool // coherence uncertainty, even in YOLO
	ReviewGate      func(agent.Exec) agent.Exec
	ApproveContract func(context.Context, string, string) error
	OnTaskSettled   func(context.Context, *plan.Task) error
}

// Result is one finished subagent run. Err is task failure, never launch
// failure — the launch itself reports only launch-level errors. Attempts
// counts runs of the task, including retries.
type Result struct {
	State              string
	Outcome            outcome.Outcome
	Verification       outcome.Verification
	VerificationReason string
	Review             string
	AttemptLog         string
	Transcript         []llm.Message
	Reused             bool
	TaskID             string
	Title              string
	Output             string
	ExecutionRequest   string // actual role request, separate from the model's output
	ExecutionEvidence  string // host audit, never evidence of requested behavior by itself
	Attempts           int
	// Skipped means this invocation did not schedule execution: either
	// queued under a pause or a reused settled task. It does not imply success.
	Skipped bool
	Err     error
	brief   *plan.Task
}

// Launch runs every task in a ready approach and collects outputs in task
// order. The approach moves ready→running; tasks run with retries; a
// clean sweep completes the approach, failures leave it running with
// failed tasks. Per-task failures ride in Results — only launch-level
// failures return err.
func Launch(ctx context.Context, d Deps, approachID string) ([]Result, error) {
	return LaunchChecked(ctx, d, approachID, "")
}

// LaunchChecked atomically refuses brief changes after readiness/approval.
func LaunchChecked(ctx context.Context, d Deps, approachID, snapshot string) ([]Result, error) {
	if d.Prompts == nil {
		pc, e := prompts.Legacy(d.TemplatesDir)
		if e != nil {
			return nil, e
		}
		d.Prompts = pc
	}

	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return nil, err
	}
	if a.State != plan.ApproachReady {
		return nil, fmt.Errorf("approach %q is %s — launch needs ready", approachID, a.State)
	}
	if err := admissionCommands(d, approachID); err != nil {
		return nil, err
	}
	if _, err := plan.TransitionApproachChecked(d.Store, approachID, plan.ApproachRunning, snapshot); err != nil {
		return nil, err
	}
	return runAndSettle(ctx, d, approachID, false)
}

// Resume re-enters a paused approach: queued tasks run, done and
// failed tasks are left alone. Skipped tasks from the pause own the
// resume — nothing reruns.
func Resume(ctx context.Context, d Deps, approachID string) ([]Result, error) {
	if d.Prompts == nil {
		pc, e := prompts.Legacy(d.TemplatesDir)
		if e != nil {
			return nil, e
		}
		d.Prompts = pc
	}

	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return nil, err
	}
	if a.State != plan.ApproachPaused {
		return nil, fmt.Errorf("approach %q is %s — resume needs paused", approachID, a.State)
	}
	if err := admissionCommands(d, approachID); err != nil {
		return nil, err
	}
	if _, err := plan.TransitionApproach(d.Store, approachID, plan.ApproachRunning); err != nil {
		return nil, err
	}
	return runAndSettle(ctx, d, approachID, true)
}

// Pause stands a running approach down at task boundaries: in-flight
// tasks finish, queued ones stay queued under the paused state.
func Pause(s *store.Store, approachID string) error {
	a, err := plan.GetApproach(s, approachID)
	if err != nil {
		return err
	}
	if a.State != plan.ApproachRunning {
		return fmt.Errorf("approach %q is %s — pause needs running", approachID, a.State)
	}
	_, err = plan.TransitionApproach(s, approachID, plan.ApproachPaused)
	return err
}

// runAndSettle fans out the approach's queued tasks, settles their
// nodes, queues the verification follow-up, and completes on a clean
// sweep. A pause mid-flight settles finished tasks and returns with
// the rest skipped — the approach stays paused.
func runAndSettle(ctx context.Context, d Deps, approachID string, resume bool) ([]Result, error) {
	if d.Prompts == nil {
		pc, e := prompts.Legacy(d.TemplatesDir)
		if e != nil {
			return nil, e
		}
		d.Prompts = pc
	}
	tasks, err := plan.ApproachTasks(d.Store, approachID)
	if err != nil {
		return nil, err
	}
	window := d.Sub.ContextWindow
	if window <= 0 {
		window = d.ParentWindow
	}
	delay := time.Duration(d.Wf.RetryDelaySeconds) * time.Second
	var results []Result
	if d.Sub.Mode == config.SubagentsSequential {
		results, err = runSequential(ctx, d, approachID, tasks, window, delay, resume)
		if err != nil {
			return results, err
		}
	} else {
		results = fanOut(ctx, d, approachID, tasks, window, delay)
	}
	var review []int
	for i := range results {
		r := &results[i]
		if d.Sub.Mode != config.SubagentsSequential && !r.Skipped {
			if err := settle(d.Store, r, ctx); err != nil {
				return results, err
			}
		}
		if d.Sub.Mode != config.SubagentsSequential && r.Outcome.Status == outcome.Completed && r.Verification == outcome.Unverified && (!r.Reused || resume) {
			review = append(review, i)
		}
	}
	// Required policy belongs to the admitted execution, not current config.
	if err := verifyPass(ctx, d, results, review, window); err != nil {
		return results, err
	}
	if d.Sub.Mode != config.SubagentsSequential {
		for i := range results {
			if !results[i].Skipped {
				if err := notifyTask(ctx, d, results[i].TaskID); err != nil {
					return results, err
				}
			}
		}
	}
	a, err := plan.GetApproach(d.Store, approachID)
	if err != nil {
		return results, err
	}
	if a.State == plan.ApproachPaused {
		return results, nil
	}
	all := len(results) > 0
	for _, r := range results {
		if !r.Qualified() {
			all = false
		}
	}
	if all {
		if _, err := plan.TransitionApproach(d.Store, approachID, plan.ApproachComplete); err != nil {
			return results, err
		}
	}
	return results, nil
}

// Ordered work admits the next worker only after the predecessor's owning
// check/review settlement. Unqualified work stays visible; later tasks stay queued.
func runSequential(ctx context.Context, d Deps, approachID string, tasks []*plan.Task, window int, delay time.Duration, resume bool) ([]Result, error) {
	results := make([]Result, len(tasks))
	hold := ""
	var runErr error
	for i, task := range tasks {
		if hold != "" || pausedNow(d.Store, approachID) {
			results[i] = resultOf(task)
			results[i].Reused = false
			results[i].VerificationReason = hold
			continue
		}
		results[i] = resultOf(task)
		if task.State == plan.TaskQueued {
			results[i] = runOne(ctx, d, task, window, d.Wf.MaxRetries, delay)
			if !results[i].Skipped {
				runErr = settle(d.Store, &results[i], ctx)
			} else {
				runErr = results[i].Err
			}
		}
		result := &results[i]
		if runErr == nil && result.Outcome.Status == outcome.Completed && result.Verification == outcome.Unverified && (!result.Reused || resume) {
			runErr = verifyPass(ctx, d, results, []int{i}, window)
		}
		if runErr == nil && !result.Skipped {
			runErr = notifyTask(ctx, d, result.TaskID)
		}
		if runErr != nil || !result.Qualified() {
			hold = fmt.Sprintf("not scheduled: preceding task %s is not qualified (execution=%s verification=%s)", result.TaskID, result.Outcome.Status, result.Verification)
		}
	}
	return results, runErr
}

// fanOut runs every queued task under the subagent cap, collecting in
// task order. Done and failed tasks are left alone; a task that finds
// the approach paused before starting skips untouched.
func fanOut(ctx context.Context, d Deps, approachID string, tasks []*plan.Task, window int, delay time.Duration) []Result {
	results := make([]Result, len(tasks))
	run := func(i int, t *plan.Task) {
		if t.State != plan.TaskQueued {
			results[i] = resultOf(t)
			return
		}
		if pausedNow(d.Store, approachID) {
			results[i] = Result{TaskID: t.ID, Title: t.Title, State: t.State, Outcome: outcome.Outcome{Status: t.Execution, Reason: t.ExecutionReason}, Verification: t.Verification, Skipped: true}
			return
		}
		results[i] = runOne(ctx, d, t, window, d.Wf.MaxRetries, delay)
	}
	switch d.Sub.Mode {
	case config.SubagentsSequential:
		for i, t := range tasks {
			run(i, t)
		}
	case config.SubagentsConcurrent:
		var wg sync.WaitGroup
		sem := make(chan struct{}, subCap(d))
		for i, t := range tasks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					run(i, t)
					return
				}
				run(i, t)
			}()
		}
		wg.Wait()
	default:
		for i, t := range tasks {
			results[i] = Result{TaskID: t.ID, Title: t.Title,
				Err: fmt.Errorf("unknown subagents mode %q", d.Sub.Mode), Outcome: outcome.Outcome{Status: outcome.Failed, Reason: "invalid subagent mode"}, Verification: outcome.Unverified}
		}
	}
	return results
}

// pausedNow reads the approach state fresh: the pause key (TUI, model
// tool) lands in the store, and the next unscheduled task sees it.
func pausedNow(s *store.Store, approachID string) bool {
	a, err := plan.GetApproach(s, approachID)
	return err == nil && a.State == plan.ApproachPaused
}

// subCap resolves the concurrent fan-out cap: explicit setting wins,
// else hardware autodetect. Reviewers share the cap with executors.
func subCap(d Deps) int {
	if d.Sub.MaxSubagents > 0 {
		return d.Sub.MaxSubagents
	}
	return autodetect()
}

// RunTask runs one task's subagent once. No transitions — the caller
// owns lifecycle; the retry loop in runOne is that caller in launches.
func RunTask(ctx context.Context, d Deps, t *plan.Task, window int) Result {
	if d.Prompts == nil {
		pc, err := prompts.Legacy(d.TemplatesDir)
		if err != nil {
			return Result{TaskID: t.ID, Title: t.Title, Err: err, Outcome: outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}}
		}
		d.Prompts = pc
	}
	brief := *t
	r := Result{State: plan.TaskRunning, brief: &brief, TaskID: t.ID, Title: t.Title, Outcome: outcome.Outcome{Status: outcome.Failed}, Verification: t.Verification, AttemptLog: t.AttemptLog, Attempts: t.Attempts}
	goal, strategy, err := parentRequirements(d, t)
	if err != nil {
		r.Err, r.Outcome.Reason = err, err.Error()
		return r
	}
	prompt, err := d.Prompts.Render("templates.subagent", map[string]string{
		"PlanGoal": goal, "ApproachTitle": strategy,
		"Title": t.Title, "Prompt": t.Prompt, "Current": t.Current,
		"Target": t.Target, "Sketch": t.Sketch, "Acceptance": t.Acceptance,
		"Tests": t.Tests, "Verify": t.Verify, "Workspace": d.Workspace, "CheckCommand": t.CheckCommand, "AcceptanceCommand": t.AcceptanceCommand,
	})
	if err != nil {
		r.Err = err
		r.Outcome.Reason = err.Error()
		return r
	}
	sub := roleRunner(d, window)
	request := prompt
	var recordingErr error
	if d.Store != nil {
		current, err := plan.GetTask(d.Store, t.ID)
		if err != nil {
			r.Err = err
			r.Outcome.Reason = err.Error()
			return r
		}
		if current.State == plan.TaskRunning && current.Attempts > 0 {
			if !current.SameBrief(t) {
				r.Err = fmt.Errorf("worker brief changed before task instructions were installed")
				r.Outcome.Reason = r.Err.Error()
				return r
			}
			r.Attempts, r.AttemptLog = current.Attempts, current.AttemptLog
			r.brief = current
			sub.SessionID, sub.Session = current.ID, reviewSink{d: d, r: &r, phase: "execution_pending", err: &recordingErr}
			admit := func(ctx context.Context) error {
				if recordingErr != nil {
					return fmt.Errorf("worker durable recording failed: %w", recordingErr)
				}
				if err := plan.PausePendingRevision(plan.WithProject(ctx, d.Workspace), d.Store, current.ID, current.Attempts); err != nil {
					return err
				}
				actual, err := plan.GetTask(d.Store, current.ID)
				if err != nil {
					return err
				}
				if actual.State != plan.TaskRunning || actual.Attempts != current.Attempts || !actual.SameBrief(current) {
					return fmt.Errorf("worker task/brief/attempt changed before execution")
				}
				currentGoal, currentStrategy, err := parentRequirements(d, actual)
				if err != nil {
					return err
				}
				if goal != currentGoal || strategy != currentStrategy {
					return fmt.Errorf("worker parent requirements changed before execution")
				}
				return ctx.Err()
			}
			sub.Exec = childGate(d, func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
				if err := admit(ctx); err != nil {
					return tool.Result{}, err
				}
				return agent.Direct(ctx, def, args)
			})
			bindRoleComments(sub, d, current)
			// Only a genuine owning worker claim installs task instructions. Role
			// probes and restored historical prose cannot confer this authority.
			prompt += "\n" + d.Prompts.Text("system.task.handoff")
			sub.TaskSystem = prompt
			request = d.Prompts.Text("system.task.worker_request")
		} else {
			// A standalone role probe is not a live task admission.
			sub.Exec = func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				return tool.Result{}, fmt.Errorf("worker execution needs the actual current running task attempt")
			}
		}
	}
	out, transcript, err := sub.Run(ctx, request)
	execution := sub.Outcome
	r.Output, r.Transcript, r.Outcome, r.Err = out, transcript, execution, err
	r.ExecutionRequest, r.ExecutionEvidence = prompt, sub.Evidence
	if recordingErr != nil {
		r.Err = errors.Join(r.Err, recordingErr)
		r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: "worker durable recording failed: " + recordingErr.Error()}
	}
	return r
}

// The parent launch approval never grants child tools or the verification episode.
func childGate(d Deps, next agent.Exec) agent.Exec {
	if d.ReviewGate != nil {
		return d.ReviewGate(next)
	}
	if d.AuthMode != "" && d.AuthMode != config.AuthYOLO {
		return func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
			return tool.Result{Blocked: true, Text: "child approval gate is unavailable; no tool executed"}, nil
		}
	}
	return next
}

// roleRunner creates a headless role. Its owning worker/reviewer path adds
// task-bound persistence and per-call authorization before running it.
func roleRunner(d Deps, window int) *agent.Runner {
	return &agent.Runner{Prompts: d.Prompts,
		Model: d.NewModel(), Registry: d.NewRegistry(), Exec: agent.Direct,
		MaxSteps: d.MaxSteps, MaxRunSeconds: d.MaxRunSeconds, Out: io.Discard, MaxTokens: window,
		Judge: d.Judge, Threshold: d.Threshold,
		LoopRepeat: d.LoopRepeat, LoopStall: d.LoopStall,
		ThinkingLoop: d.ThinkingLoop,
		Compaction:   d.Compaction, TemplatesDir: d.TemplatesDir,
		Prune: d.Prune, Behavior: d.Behavior,
		// Ask nil: headless subagents stop on unsure confirms.
	}
}
func runSubagent(ctx context.Context, d Deps, prompt string, window int) (string, []llm.Message, outcome.Outcome, error) {
	sub := roleRunner(d, window)
	out, transcript, err := sub.Run(ctx, prompt)
	return out, transcript, sub.Outcome, err
}

// verifyPass queues the follow-up run: one reviewer per task that ran
// clean in this pass, briefed by its verify field. A FAIL verdict
// stores the review and marks the task failed. A missing or interrupted
// reviewer preserves completed execution as unverified, holding completion.
func verifyPass(ctx context.Context, d Deps, results []Result, indices []int, window int, supplied ...*session.CodingState) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, subCap(d))
	errs := make([]error, len(indices))
	for j, i := range indices {
		wg.Add(1)
		go func(j, i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].Verification = outcome.Unverified
				results[i].VerificationReason = "cancelled before reviewer admission"
				errs[j] = errors.Join(ctx.Err(), settle(d.Store, &results[i], ctx))
				return
			}
			r := &results[i]
			t, err := plan.GetTask(d.Store, r.TaskID)
			if err != nil {
				errs[j] = err
				return
			}
			r.Verification = outcome.Unverified
			var coding *session.CodingState
			var check outcome.Verification
			if len(supplied) > i {
				coding = supplied[i]
				check, err = recordCurrentCheck(d, r, t, coding)
			} else {
				check, err = runCheck(ctx, d, r, t)
				coding = codingFromCheck(d, r, t)
			}
			if err != nil {
				errs[j] = errors.Join(err, settle(d.Store, r, ctx))
				return
			}
			checkReason := r.VerificationReason
			if check == outcome.Rejected {
				r.Verification = check
				r.Err = fmt.Errorf("required check failed: %s", checkReason)
				errs[j] = settle(d.Store, r, ctx)
				return
			}
			record := attemptRecord{Kind: "review", Attempt: r.Attempts, Outcome: outcome.Outcome{Status: outcome.Unknown}}
			if strings.TrimSpace(t.Verify) == "" {
				r.VerificationReason = "required verification instructions are empty"
			} else {
				checkFacts, err := json.Marshal(coding)
				if err != nil {
					errs[j] = err
					return
				}
				brief := r.brief
				if brief == nil {
					brief = t
				}
				goal, strategy, err := parentRequirements(d, brief)
				if err != nil {
					errs[j] = errors.Join(err, settle(d.Store, r, ctx))
					return
				}
				prompt, err := d.Prompts.Render("templates.review", map[string]string{
					"PlanGoal": goal, "ApproachTitle": strategy,
					"Title": brief.Title, "Prompt": brief.Verify, "Current": r.Output,
					"TaskPrompt": brief.Prompt, "TaskCurrent": brief.Current, "Target": brief.Target,
					"Sketch": brief.Sketch, "Tests": brief.Tests, "Acceptance": brief.Acceptance,
					"Workspace": d.Workspace, "CheckCommand": brief.CheckCommand, "AcceptanceCommand": brief.AcceptanceCommand,
					"ExecutionRequest": r.ExecutionRequest, "ExecutionEvidence": r.ExecutionEvidence,
					"CheckReceipt": string(checkFacts),
				})
				if err != nil {
					r.VerificationReason = err.Error()
				} else {
					reply, transcript, execution, err := runReviewer(ctx, d, prompt, window, r, t, coding)
					record.Output, record.Transcript, record.Outcome = reply, transcript, execution
					if err != nil {
						record.Error = err.Error()
					}
					r.Review = reply
					if err != nil || execution.Status != outcome.Completed {
						r.VerificationReason = "review " + string(execution.Status) + ": " + execution.Reason
						if err != nil {
							r.VerificationReason += "; " + err.Error()
						}
						if execution.Status == outcome.Cancelled {
							r.VerificationReason = "cancelled: " + r.VerificationReason
						}
					} else {
						r.Verification = reviewVerdict(reply)
						switch {
						case r.Verification == outcome.Passed && check != outcome.Passed:
							r.Verification = outcome.Unverified
							r.VerificationReason = checkReason + "; model reported PASS"
						case r.Verification == outcome.Unverified:
							r.VerificationReason = checkReason + "; review did not return an explicit PASS or FAIL"
						default:
							r.VerificationReason = checkReason + "; model reported " + string(r.Verification)
						}
					}
				}
			}

			pending, e := popPending(r, "review_pending")
			if e != nil {
				errs[j] = e
				return
			}
			record.ReviewerEvents = pending.ReviewerEvents
			reviewReason := r.VerificationReason
			current, saveErr := recordCurrentCheck(d, r, t, coding)
			if saveErr != nil {
				errs[j] = saveErr
				return
			}
			r.VerificationReason = reviewReason
			if r.Verification == outcome.Passed && current != outcome.Passed {
				r.Verification = current
				r.VerificationReason = "review PASS lacks a current declared check after reviewer effects"
			}
			record.Verification = r.Verification
			if record.Outcome.Status == outcome.Unknown {
				record.Outcome.Reason = r.VerificationReason
			}
			if err := appendReview(r, record); err != nil {
				errs[j] = err
				return
			}
			if r.Verification != outcome.Passed {
				r.Err = fmt.Errorf("verification %s: %s", r.Verification, r.VerificationReason)
				if r.Verification == outcome.Rejected && reviewVerdict(r.Review) == outcome.Rejected {
					r.Err = fmt.Errorf("review FAIL: %s", r.Review)
				}
			}
			errs[j] = settle(d.Store, r, ctx)
		}(j, i)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// isFail reads the reviewer's one-line verdict: FAIL leads.
func reviewVerdict(out string) outcome.Verification {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return outcome.Unverified
	}
	token := strings.ToUpper(strings.SplitN(fields[0], ":", 2)[0])
	switch token {
	case "PASS":
		return outcome.Passed
	case "FAIL":
		return outcome.Rejected
	default:
		return outcome.Unverified
	}
}
func isFail(out string) bool { return reviewVerdict(out) == outcome.Rejected }

// runOne runs one task with retries: each attempt walks the lifecycle
// table (→running, attempt, →failed→queued on failure), and the node
// keeps the final attempt count on success or the full attempt log on
// exhaustion. Every attempt retains its raw output, transcript and cause.
func runOne(ctx context.Context, d Deps, t *plan.Task, window, retries int, delay time.Duration) Result {
	var attempts []json.RawMessage
	if t.AttemptLog != "" {
		if err := json.Unmarshal([]byte(t.AttemptLog), &attempts); err != nil {
			return admissionFailure(t, fmt.Errorf("cannot read attempt log: %w", err))
		}
	}
	// Preserve existing artifacts without inventing an execution/review trace.
	if len(attempts) == 0 && (t.Result != "" || t.Review != "") {
		log, err := outcome.AppendRecord(t.AttemptLog, attemptRecord{Kind: "prior_state", Attempt: t.Attempts, Outcome: outcome.Outcome{Status: t.Execution, Reason: t.ExecutionReason}, Verification: t.Verification, Output: t.Result, Review: t.Review})
		if err != nil {
			return admissionFailure(t, err)
		}
		t.AttemptLog = log
	}
	base := t.Attempts
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			r := resultOf(t)
			r.Skipped, r.Reused = false, false
			r.Outcome = outcome.Outcome{Status: outcome.Cancelled, Reason: err.Error()}
			r.Verification, r.VerificationReason, r.Err = outcome.Unverified, "execution cancelled before admission", err
			return r
		}
		if d.ApproveContract != nil {
			snapshot, _, err := plan.ContractSnapshot(plan.WithProject(ctx, d.Workspace), d.Store, "task", t.ID)
			if err != nil {
				return heldTask(t, err)
			}
			if err = approveTaskContracts(ctx, d, t.ID); err != nil {
				return heldTask(t, err)
			}
			claimed, err := plan.ClaimTaskContractContext(plan.WithProject(ctx, d.Workspace), d.Store, t.ID, snapshot, "")
			if err != nil {
				return heldTask(t, err)
			}
			t = claimed
		} else if _, err := plan.TransitionTask(d.Store, t.ID, plan.TaskRunning); err != nil {
			return admissionFailure(t, err)
		}
		t.Verification = outcome.Unverified
		if !d.Wf.VerifyFollowup && strings.TrimSpace(t.CheckCommand) == "" {
			t.Verification = outcome.NotRequired
		}
		t.Execution, t.ExecutionReason, t.VerificationReason = outcome.Unknown, "", ""
		if err := plan.SetTaskOutcome(d.Store, t); err != nil {
			return admissionFailure(t, err)
		}
		if err := plan.SetTaskAttempts(d.Store, t.ID, base+n); err != nil {
			return admissionFailure(t, err)
		}
		current, err := plan.GetTask(d.Store, t.ID)
		if err != nil {
			return admissionFailure(t, err)
		}
		t = current
		r := RunTask(ctx, d, t, window)
		r.Attempts = base + n
		r.Verification = t.Verification
		errorText := ""
		if r.Err != nil {
			errorText = r.Err.Error()
		}
		pending, err := popPending(&r, "execution_pending")
		if err != nil {
			r.Err = err
			return r
		}
		if err := appendReview(&r, attemptRecord{Attempt: r.Attempts, Kind: "execution", Verification: r.Verification, Outcome: r.Outcome, Output: r.Output, Error: errorText, Transcript: r.Transcript, ExecutionEvents: pending.ExecutionEvents}); err != nil {
			r.Err = err
			return r
		}
		t.Result, t.Review, t.Attempts, t.AttemptLog = r.Output, r.Review, r.Attempts, r.AttemptLog
		if r.Outcome.Status != outcome.Failed || n > retries {
			return r
		}
		if err := settle(d.Store, &r, ctx); err != nil {
			r.Err = err
			return r
		}
		if _, err := plan.TransitionTask(d.Store, t.ID, plan.TaskQueued); err != nil {
			r.Err = err
			return r
		}
		r.State = plan.TaskQueued
		if delay > 0 {
			select {
			case <-ctx.Done():
				r.Outcome = outcome.Outcome{Status: outcome.Cancelled, Reason: ctx.Err().Error()}
				r.Err = ctx.Err()
				return r
			case <-time.After(delay):
			}
		}
	}
}

// autodetect resolves the concurrent cap from hardware. Detection fails
// only on unsupported platforms; there the documented floor holds.
func autodetect() int {
	p, err := hw.Detect()
	if err != nil {
		return 2
	}
	return hw.MaxSubagents(p)
}

// New builds the launch tool: one call fans out an approach by id.
func New(d Deps) tool.Definition {
	return tool.Definition{
		Name: "launch", WorkflowRefArg: "approach", ResolveWorkflowRef: workflowRef(d),
		Label:   "launch",
		Desc:    d.Prompts.Text("tools.launch.description"),
		Purpose: d.Prompts.Text("tools.launch.purpose"),
		Params: []tool.Param{
			{Name: "approach", Desc: d.Prompts.Text("tools.launch.parameters.approach"), Type: tool.TypeString, Required: true},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			id, _ := args["approach"].(string)
			if d.Store == nil || d.NewModel == nil || d.NewRegistry == nil {
				return workflowResult(d, id, nil, fmt.Errorf("launch tool is not wired — needs store, model, and registry"))
			}
			snapshot, err := CheckReady(ctx, d, id)
			if err != nil {
				return workflowResult(d, id, nil, err)
			}
			tasks, err := plan.ApproachTasks(d.Store, id)
			if err != nil {
				return workflowResult(d, id, nil, err)
			}
			if err := approveLaunch(ctx, d, id, len(tasks)); err != nil {
				return workflowResult(d, id, nil, err)
			}
			results, err := LaunchChecked(ctx, d, id, snapshot)
			return workflowResult(d, id, results, err)
		},
	}
}

// format renders the parent feed: counts plus per-task outputs truncated
// to maxChars each. Full outputs stay on their task nodes.
func format(approachID string, results []Result, maxChars int) string {
	counts := countResults(results)
	var b strings.Builder
	fmt.Fprintf(&b, "approach %s: %d/%d tasks qualified; %d executed, %d verified, %d unverified, %d failed, %d stopped, %d cancelled, %d queued", approachID, counts.Qualified, len(results), counts.Executed, counts.Verified, counts.Unverified, counts.Failed, counts.Stopped, counts.Cancelled, counts.Queued)
	for _, r := range results {
		text := resultText(r.Output, maxChars)
		fmt.Fprintf(&b, "\n%s %s (%s): execution=%s verification=%s", r.State, r.Title, tries(r.Attempts), r.Outcome.Status, r.Verification)
		if r.Skipped {
			b.WriteString(" (reused/not scheduled)")
		}
		if text != "" {
			b.WriteString("\n" + text)
		}
		if r.Outcome.Reason != "" {
			b.WriteString("\nexecution reason: " + r.Outcome.Reason)
		}
		if r.VerificationReason != "" {
			b.WriteString("\nverification reason: " + r.VerificationReason)
		}
		if r.brief != nil && r.brief.CheckCommand != "" {
			b.WriteString("\ndeclared check: " + resultText(r.brief.CheckCommand, maxChars))
		}
		if r.Review != "" {
			b.WriteString("\nmodel review: " + resultText(r.Review, maxChars))
		}
	}
	return b.String()
}

// tries phrases an attempt count for the parent feed.
func tries(n int) string {
	if n == 1 {
		return "1 attempt"
	}
	return fmt.Sprintf("%d attempts", n)
}

func resultText(text string, maxChars int) string {
	runes := []rune(text)
	if maxChars > 0 && len(runes) > maxChars {
		return string(runes[:maxChars]) + "…[truncated, full text on the task node]"
	}
	return text
}

// Empty ref means known invalid; an uncertain lookup retains its obligation.
func workflowRef(d Deps) func(context.Context, tool.Args) (string, error) {
	return func(ctx context.Context, args tool.Args) (string, error) {
		id, _ := args["approach"].(string)
		if err := ctx.Err(); err != nil {
			return id, err
		}
		if d.Store == nil {
			return id, fmt.Errorf("workflow owner store is not wired")
		}
		a, err := plan.GetApproach(d.Store, id)
		if errors.Is(err, plan.ErrNotFound) {
			return "", err
		}
		if err != nil {
			return id, err
		}
		return a.ID, nil
	}
}

func heldTask(t *plan.Task, err error) Result {
	r := resultOf(t)
	r.Reused = false
	r.Err = err
	r.VerificationReason = "task admission withheld: " + err.Error()
	return r
}
func approveTaskContracts(ctx context.Context, d Deps, id string) error {
	if d.ApproveContract == nil {
		return ctx.Err()
	}
	a, err := plan.TaskApproach(d.Store, id)
	if err != nil {
		return err
	}
	p, err := plan.ApproachPlan(d.Store, a.ID)
	if err != nil {
		return err
	}
	for _, scope := range []struct{ kind, id string }{{"plan", p.ID}, {"approach", a.ID}, {"task", id}} {
		if err = d.ApproveContract(ctx, scope.kind, scope.id); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func notifyTask(ctx context.Context, d Deps, id string) error {
	if d.OnTaskSettled == nil {
		return nil
	}
	t, err := plan.GetTask(d.Store, id)
	if err != nil {
		return err
	}
	return d.OnTaskSettled(ctx, t)
}
