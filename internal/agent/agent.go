// Package agent is the headless ReAct loop: model thinks, emits tool
// blocks, the harness executes, results feed back — until the model
// answers without a tool block or the step cap stops the loop plainly.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"specific-harness/internal/prompts"
	"strings"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Model is anything that chats. *llm.Client satisfies it.
type Model interface {
	Chat(ctx context.Context, msgs []llm.Message) (reply, served string, usage llm.Usage, err error)
}

// Streamer is a Model that streams deltas with usage. The loop uses
// it when OnToken is set; plain Chat otherwise.
type Streamer interface {
	Model
	ChatStream(ctx context.Context, msgs []llm.Message, onDelta func(string)) (string, string, llm.Usage, error)
}

// Exec runs a validated tool call. Swappable so auth gates (T1.6) wrap it.
type Exec func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error)

// Direct executes without gating.
func Direct(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return def.Execute(ctx, args)
}

// Runner drives the loop.
type Runner struct {
	Prompts *prompts.Catalog
	// Outcome is the final execution fact; it is reset on every Run.
	Assigned            *AssignedOps
	assignedClaim       bool
	claimPreparationEnd int // pending settled-claim cleanup; never restored authority
	settlement          *TaskSettlement
	phaseRegistry       *tool.Registry // unprojected owning definitions, not execution authority
	phaseView           *tool.Registry
	progressWatch       *watcher // current run only; one evidence window for advisory touchpoints
	enforcement         enforcement
	// StyleEnforcement, when true with a non-empty Styleguide, sends a
	// validated task to the style judge once per run before handoff.
	StyleEnforcement       bool
	Styleguide             string // current workspace document head; never authority
	styleRounds            int    // send-back rounds already used this run
	preparationDisposition string // latest settled preparation reply; no judge evidence or restored authority
	nativeCharges          int    // actual invocations/refusal work; an interrupted array tail is not charged
	Outcome                outcome.Outcome
	Evidence               string // actual tool facts, separate from the model-reported final
	workflows              map[string]outcome.Report
	Model                  Model
	Registry               *tool.Registry
	WorkflowView           func(*session.WorkflowScope) (*tool.Registry, string, error)
	PlanningCompletion     func(*session.WorkflowScope) (*outcome.Report, error)
	pendingFocus           *session.WorkflowScope
	workflowHeader         string
	preparationQuestion    string
	Exec                   Exec
	MaxSteps               int
	MaxRunSeconds          int
	CodingRequired         bool
	Workspace              string
	CheckCommand           string
	hostCheckDefinition    tool.Definition // factory-owned; independent of the model-visible projection
	codingErr              error
	fileEvidence           fileEvidence
	Out                    io.Writer // assistant text streams here as it arrives
	// OnToken receives assistant deltas as they stream. Nil = off.
	// The model must satisfy Streamer, else the loop uses Chat.
	OnToken func(string)
	// SessionID + Session persist the run event by event. A nil Session
	// means no persistence. A persistence failure does not stop the run,
	// but it does fail the returned error — a lost record stays loud.
	SessionID string
	Session   SessionLog
	// MaxTokens caps the run at the window: the loop stops once the
	// server's last reported prompt size reaches it. 0 means unlimited.
	MaxTokens int
	// UsedIn/UsedOut accumulate server-reported tokens across the run;
	// UsageUnknown reports that some exchange omitted usage (totals then
	// undercount). Read after Run returns.
	UsedIn       int
	UsedOut      int
	UsageUnknown bool
	// Judge resolves enabled guards and admission; thinking/compaction refuse if missing.
	Judge           JudgeFunc
	ManualDecisions bool // decision opt-out requests human admission through Ask
	// Threshold belongs to factual admission; advisory supervision uses the owner's 0.5 floor.
	Threshold float64
	Ask       func(ctx context.Context, prompt string) bool
	// LoopRepeat/LoopStall schedule advisory progress judgments. Neither is a call/error ceiling.
	// Zero disables that cadence; actual authority and stream guards are separate.
	LoopRepeat   int
	LoopStall    int
	ThinkingLoop ThinkingLoop
	// JudgmentContextChars caps the context kept in a loop-confirm
	// judgment event. 0 = whole.
	JudgmentContextChars int
	// Compaction drives mid-run context compaction; Trigger 0 disables.
	// TemplatesDir is a legacy directory selector for the same prompts.yaml.
	Compaction   Compaction
	TemplatesDir string
	// Prune stubs old tool outputs and schema dumps; zero disables.
	Prune Prune
	// Behavior, when non-empty, injects as its own system message
	// right after the base prompt. Shaped by ShapeBehavior.
	Behavior     string
	BehaviorNote string // disclosed through the ordinary session note channel
	// TaskSystem is supplied by the owning worker or reviewer role. It is a
	// separate protected system entry, never restored as assignment authority.
	TaskSystem string
	Reviewing  bool // Set by the owning independent-review path, never inferred from prompt prose.
	// FinalFeedback repairs a normal final in this same run; empty accepts it.
	FinalFeedback func(string) string
	// WorkspaceExcludes contains exact harness-owned state paths.
	WorkspaceExcludes []string
	// Memories, when non-empty, injects as a user-role message right
	// after the prompt. Recalled by memory.Recall in main.
	Memories string
	// History carries prior turns (user/assistant finals) ahead of the
	// prompt so chat continues a conversation. Nil means a fresh run.
	// It compacts and prunes like any transcript middle.
	History      []llm.Message
	Continuation session.Continuation
	ModelKey     string
	// Btw pops queued user notes at a model boundary ("" = none).
	// Called from the run goroutine; must be goroutine-safe.
	Btw func() string
	// GenerationInterrupt registers only the current model request's cancellation.
	// A queued /btw must never cancel a running effect or the owning Run context.
	GenerationInterrupt func(context.CancelFunc)
	UserCommentPending  func() bool
	TaskComments        func(context.Context) ([]outcome.TaskComment, error)
	CommentReceipt      func(context.Context, outcome.TaskComment) error
	CommentAppend       func(context.Context, string) error
}

// JudgeFunc asks the decision model one question.
type JudgeFunc func(context.Context, decision.Request) (decision.Verdict, error)

// Run executes prompt to completion. It returns the final answer plus the
// full transcript, persisting prompt/reply/tool/result/done events when
// Session is set.
func (r *Runner) Run(ctx context.Context, prompt string) (string, []llm.Message, error) {
	if r.Prompts == nil {
		pc, err := prompts.Legacy(r.TemplatesDir)
		if err != nil {
			r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
			return "", nil, err
		}
		r.Prompts = pc
	}
	if r.Registry != nil {
		r.Registry.Prompts = r.Prompts
	}
	if r.MaxSteps < 0 {
		err := fmt.Errorf("agent.max_steps cannot be negative")
		r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
		return "", nil, err
	}
	if r.MaxRunSeconds < 0 || r.MaxRunSeconds > 9223372036 {
		err := fmt.Errorf("agent.max_run_seconds outside representable nonnegative duration")
		r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
		return "", nil, err
	}
	if r.MaxRunSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, time.Duration(r.MaxRunSeconds)*time.Second, runBudgetReached)
		defer cancel()
	}
	r.pendingFocus = nil
	r.assignedClaim = false
	r.claimPreparationEnd = 0
	r.settlement = nil
	if r.Registry == r.phaseView {
		r.Registry = r.phaseRegistry
	}
	r.phaseRegistry, r.phaseView = nil, nil
	r.enforcement = enforcement{}
	r.preparationDisposition = ""
	if r.Assigned != nil {
		r.Assigned.Reset()
		r.CheckCommand = r.Assigned.ConfiguredCommand
	}
	r.codingErr = nil
	r.fileEvidence = fileEvidence{}
	r.UsedIn, r.UsedOut, r.UsageUnknown = 0, 0, false
	r.Outcome = outcome.Outcome{Status: outcome.Unknown}
	if r.Continuation.Version > 0 {
		if err := r.Continuation.Validate(); err != nil {
			r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
			return "", nil, err
		}
	}
	r.workflows = map[string]outcome.Report{}
	r.Evidence = ""
	exec := r.Exec
	if exec == nil {
		exec = Direct
	}
	execute := r.assignmentExec(exec)
	var settled, failed, blocked, incomplete int
	checkAttempt, usedSteps := -1, 0
	var checkTask string
	var checkTaskAttempt int
	startedAt := time.Now()
	exec = func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
		res, err := execute(ctx, def, args)
		if def.Name == "check" && r.Continuation.Coding != nil {
			checkAttempt = r.Continuation.Coding.Revision
			checkTask = r.Continuation.Coding.TaskID
			checkTaskAttempt = r.Continuation.Coding.Attempt
		}
		if def.Name == "bash" {
			switch {
			case res.Blocked:
				blocked++
			case err != nil || res.ExitCode == nil:
				incomplete++
			default:
				settled++
				if *res.ExitCode != 0 || res.Failed {
					failed++
				}
			}
		}
		return res, err
	}
	out := r.Out
	if out == nil {
		out = io.Discard
	}
	var sessErr error
	var watch *watcher
	if r.Judge != nil {
		watch = &watcher{}
	}
	r.progressWatch = watch
	log := func(kind, text string) {
		if r.Session == nil || sessErr != nil {
			return
		}
		// Cancellation stops work, not the local settlement receipt. Keep the
		// store lease until its partial-result/cancelled events have been recorded.
		logCtx := ctx
		if ctx.Err() != nil {
			logCtx = context.WithoutCancel(ctx)
		}
		if err := r.Session.Append(logCtx, r.SessionID, kind, text); err != nil {
			sessErr = err
		}
	}
	boundExec := exec
	exec = func(ctx context.Context, def tool.Definition, args tool.Args) (tool.Result, error) {
		if def.Bound {
			raw, _ := json.Marshal(args)
			log("bound_call", def.Name+" "+string(raw))
			if r.Session == nil || sessErr != nil {
				return tool.Result{}, fmt.Errorf("bound call requires a durable receipt: %v", sessErr)
			}
		}
		return boundExec(ctx, def, args)
	}
	// Publish only the reading; full continuation/history remains a settlement receipt.
	contextEvent := func(reading session.Continuation) {
		raw, _ := json.Marshal(session.Continuation{Prompt: reading.Prompt,
			Measured: reading.Measured, ModelKey: reading.ModelKey})
		log("context", string(raw))
	}
	measurePrompt := func(usage llm.Usage) {
		r.Continuation.Measured = usage.Reported
		if usage.Reported {
			r.Continuation.Prompt = usage.In
		}
		contextEvent(r.Continuation)
	}
	if err := r.refreshWorkflow(); err != nil {
		r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: err.Error()}
		return "", nil, err
	}
	r.begin(prompt)
	defs, definitionErr := r.definitions()
	if definitionErr != nil {
		r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: definitionErr.Error()}
		return "", nil, definitionErr
	}
	transcript := r.Continuation.Messages()
	if r.Behavior != "" {
		log("behavior", r.Behavior)
	}
	if r.BehaviorNote != "" {
		log("note", r.BehaviorNote)
	}
	log("prompt", prompt)
	if r.Workspace != "" {
		log("workspace", r.Workspace) // Host association for selected historical reads, never model prose.
	}
	contextEvent(r.Continuation)
	if r.Memories != "" {
		log("recall", r.Memories)
	}
	add := func(kind, role, text string, unread bool) {
		if kind == "guidance" {
			var round *session.TaskRound
			if r.assignedClaim && r.settlement == nil {
				round = session.RoundFor(r.Continuation.Assignment, "")
			}
			r.Continuation.AddGuidance(text, round)
		} else {
			r.Continuation.Add(kind, role, text, unread)
		}
		transcript = r.Continuation.Messages()
	}
	deliverGuidance := func(raw, presentation string, prepare bool, otherAdvice string) error {
		if raw != "" {
			log("enforcement", raw) // Preserve the original event before scoped publication.
		}
		if sessErr != nil {
			return sessErr
		}
		delivered, err := r.publishPreparationGuidance(ctx, presentation, prepare, otherAdvice)
		if err != nil {
			return err
		}
		if delivered {
			transcript = r.Continuation.Messages()
			contextEvent(r.Continuation)
		} else if presentation != "" {
			add("guidance", "user", presentation, false)
		}
		return sessErr
	}
	decisionGuidance := func(raw string) error {
		if r.Assigned == nil || raw == "" {
			return nil
		}
		other := r.enforcement.presentation
		if r.enforcement.prepare {
			other = r.enforcement.knowledgePresentation
		}
		return deliverGuidance(raw, r.enforcement.presentation, r.enforcement.prepare, other)
	}
	reviewDiscovery := func() error {
		r.progressCheckpoint(usedSteps, startedAt)
		advice, err := r.reviewDiscovery(ctx, watch, func(k, t string) error { log(k, t); return sessErr })
		if err != nil {
			return err
		}
		if advice != "" {
			add("guidance", "user", advice, false)
		}
		return nil
	}
	thinkingStop := false
	normalFinal := false
	lastSettlementReceipt := ""
	finish := func(status outcome.Status, reason, text string, err error) (string, []llm.Message, error) {
		if errors.Is(context.Cause(ctx), runBudgetReached) {
			status = outcome.Stopped
			reason = fmt.Sprintf("agent.max_run_seconds=%d reached; generation settlement unknown", r.MaxRunSeconds)
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = outcome.Cancelled
			reason = context.Canceled.Error()
		}

		if status == outcome.Completed && sessErr != nil {
			status = outcome.Failed
			reason = sessErr.Error()
		}
		if status == outcome.Completed {
			unresolvedStatus := outcome.Failed
			var unresolved []string
			for _, report := range r.workflows {
				if !report.Qualified {
					if report.Outcome.Status == outcome.Cancelled {
						unresolvedStatus = outcome.Cancelled
					} else if report.Outcome.Status == outcome.Stopped && unresolvedStatus != outcome.Cancelled {
						unresolvedStatus = outcome.Stopped
					}
					unresolved = append(unresolved, report.Ref+": "+report.Outcome.Reason)
				}
			}
			sort.Strings(unresolved)
			if len(unresolved) > 0 {
				status = unresolvedStatus
				reason = "required workflow unresolved: " + strings.Join(unresolved, "; ")
			}
		}
		if normalFinal && status == outcome.Completed && err == nil && sessErr == nil && ctx.Err() == nil && r.FinalFeedback != nil {
			if feedback := r.FinalFeedback(text); feedback != "" {
				if r.withinStepBudget(usedSteps) {
					log("evidence", feedback)
					if sessErr == nil {
						add("evidence", "user", feedback, false)
						return text, transcript, repairFinal
					}
					status, reason, err = outcome.Failed, sessErr.Error(), sessErr
				} else {
					status, reason = outcome.Stopped, "review verdict pending; step budget exhausted"
				}
			}
		}
		planningPending := r.PlanningActive() && (!r.assignedClaim || r.settlement != nil && r.settlement.Qualified)
		planningFinal := normalFinal && status == outcome.Completed && r.Assigned != nil && planningPending
		planningQualified := false
		if status == outcome.Completed && planningPending {
			if len(r.workflows) == 0 || r.PlanningCompletion == nil {
				status, reason = outcome.Stopped, "planned work has no current qualified execution receipt"
			} else if report, e := r.PlanningCompletion(r.PlanningScope()); e != nil {
				status, reason = outcome.Failed, e.Error()
			} else if report == nil || !report.Qualified || !r.workflows[report.Ref].Qualified {
				status, reason = outcome.Stopped, "selected plan still has required unqualified work"
			} else {
				planningQualified = true
			}
		}
		planningFinal = planningFinal && !planningQualified
		validatedHandoff := false
		if status == outcome.Completed && r.CodingRequired && (!r.PlanningActive() || r.assignedClaim && (r.settlement == nil || !r.settlement.Qualified)) {
			c := r.Continuation.Coding
			if (!r.PlanningActive() || r.assignedClaim) && strings.TrimSpace(text) != "" && strings.TrimSpace(r.CheckCommand) != "" &&
				r.Session != nil && sessErr == nil && r.codingErr == nil && ctx.Err() == nil &&
				r.withinStepBudget(usedSteps) && c != nil && !c.Passed() &&
				(checkTask != c.TaskID || checkTaskAttempt != c.Attempt || checkAttempt != c.Revision) {
				usedSteps++ // Host work consumes the same remaining step budget.
				obs, hostErr := r.hostCheck(ctx, exec, func(kind, text string) error { log(kind, text); return sessErr })
				if obs != "" {
					add("evidence", "user", "Host verification episode (harness-triggered):\n"+obs, false)
				}
				if hostErr != nil {
					status, reason, err = outcome.Failed, hostErr.Error(), errors.Join(err, hostErr)
				}
				if errors.Is(context.Cause(ctx), runBudgetReached) {
					status, reason = outcome.Stopped, fmt.Sprintf("agent.max_run_seconds=%d reached during host check", r.MaxRunSeconds)
				} else if ctx.Err() != nil {
					status, reason, err = outcome.Cancelled, ctx.Err().Error(), errors.Join(err, ctx.Err())
				}
			}
			if status == outcome.Completed {
				status, reason = r.codingOutcome()
			}
			if r.codingErr != nil {
				err = errors.Join(err, r.codingErr)
			}
		}

		if r.Assigned != nil && r.assignedClaim {
			settleCtx := ctx
			if status != outcome.Completed {
				settleCtx = context.WithoutCancel(ctx)
			}
			settlement, settleErr := r.Assigned.Settle(settleCtx, outcome.Outcome{Status: status, Reason: reason}, text, prompt, r.completionEvidence(), transcript, r.Continuation.Coding)
			if settleErr != nil {
				status, reason, err = outcome.Failed, settleErr.Error(), errors.Join(err, settleErr)
			} else {
				r.settlement = &settlement
				a := r.Continuation.Assignment
				receipt := fmt.Sprintf("Assigned task settlement: task=%s attempt=%d qualified=%t (owner current-check and independent-review contract; false means not qualified).", a.TaskID, a.Attempt, settlement.Qualified)
				newSettlement := receipt != lastSettlementReceipt
				if newSettlement {
					log("evidence", receipt)
					if sessErr == nil {
						add("evidence", "user", receipt, false)
						lastSettlementReceipt = receipt
					}
				}
				if settlement.Qualified && status == outcome.Completed && ctx.Err() == nil && sessErr == nil && settlement.TaskID == a.TaskID && settlement.Attempt == a.Attempt {
					revision := r.Continuation.Revision
					if cleanupErr := r.retireTaskGuidance(ctx, session.RoundFor(a, "")); cleanupErr != nil {
						status, reason, err = outcome.Failed, cleanupErr.Error(), errors.Join(err, cleanupErr)
					} else {
						transcript = r.Continuation.Messages()
						if r.Continuation.Revision != revision {
							contextEvent(r.Continuation)
						}
					}
				}
				validatedHandoff = newSettlement && settlement.Qualified && normalFinal && status == outcome.Completed
				if newSettlement && normalFinal && settlement.rejected() && err == nil && ctx.Err() == nil && sessErr == nil && r.codingErr == nil && r.withinStepBudget(usedSteps) {
					feedback := fmt.Sprintf("REMEDIATE task=%s attempt=%d state=%s check=%s verification=%s:\n%s\n", settlement.TaskID, settlement.Attempt, settlement.State, settlement.Check, settlement.Verification, settlement.Feedback) + r.Prompts.Text("continuation.remediate")
					log("evidence", feedback)
					if sessErr == nil {
						add("evidence", "user", feedback, false)
						return text, transcript, repairFinal
					}
				}
				if status == outcome.Completed && !settlement.Qualified {
					status, reason = outcome.Stopped, "assigned task remains unqualified after actual review"
				}
			}
		}
		if normalFinal && err == nil && !planningQualified && sessErr == nil && r.codingErr == nil && ctx.Err() == nil && (status == outcome.Completed || r.Assigned != nil && planningPending) {
			repair, e := r.enforce(ctx, true, text, func(k, t string) error { log(k, t); return sessErr })
			if e != nil {
				status, reason, err = outcome.Failed, e.Error(), errors.Join(err, e)
			}
			if repair != "" && e == nil && !r.withinStepBudget(usedSteps) {
				status, reason = outcome.Stopped, "bookkeeping repair pending; step budget exhausted"
			}
			if repair != "" && r.withinStepBudget(usedSteps) && e == nil {
				if guidanceErr := decisionGuidance(repair); guidanceErr == nil {
					return text, transcript, repairFinal
				} else {
					status, reason, err = outcome.Failed, guidanceErr.Error(), errors.Join(err, guidanceErr)
				}
			}
		}
		if validatedHandoff && status == outcome.Completed && err == nil && sessErr == nil && r.codingErr == nil && ctx.Err() == nil && r.withinStepBudget(usedSteps) {
			if sendback := r.styleSendback(ctx, text, func(k, t string) error { log(k, t); return sessErr }); sendback != "" && sessErr == nil {
				add("evidence", "user", sendback, false)
				return text, transcript, repairFinal
			}
			s := r.settlement
			progress := fmt.Sprintf("VALIDATED task=%s attempt=%d by owning current check and independent review.", s.TaskID, s.Attempt) + r.Prompts.Text("continuation.validated")
			log("evidence", progress)
			if sessErr == nil {
				add("evidence", "user", progress, false)
				return text, transcript, repairFinal
			}
		}
		if validatedHandoff && status == outcome.Completed && r.PlanningActive() && !r.withinStepBudget(usedSteps) {
			status, reason = outcome.Stopped, "verified task; parent handoff pending; step budget exhausted"
		}
		if planningFinal && err == nil && sessErr == nil && r.codingErr == nil && ctx.Err() == nil {
			if r.withinStepBudget(usedSteps) {
				request := r.Prompts.Text("continuation.no_claim_final")
				if guidanceErr := deliverGuidance(request, request, true, ""); guidanceErr == nil {
					return text, transcript, repairFinal
				} else {
					status, reason, err = outcome.Failed, guidanceErr.Error(), errors.Join(err, guidanceErr)
				}
			}
			if err == nil {
				status, reason = outcome.Stopped, "selected planning work has no current qualified execution receipt"
			}
		}
		if errors.Is(context.Cause(ctx), runBudgetReached) {
			status, reason = outcome.Stopped, fmt.Sprintf("agent.max_run_seconds=%d reached during settlement", r.MaxRunSeconds)
		} else if ctx.Err() != nil {
			status, reason, err = outcome.Cancelled, ctx.Err().Error(), errors.Join(err, ctx.Err())
		}
		switch status {
		case outcome.Completed:
			log("done", text)
		case outcome.Stopped:
			log("stopped", reason)
		case outcome.Cancelled:
			log("cancelled", reason)
		case outcome.Failed:
			if err == nil {
				log("failed", reason)
			}
		}
		if sessErr != nil {
			if err == nil {
				err = sessErr
			} else if !errors.Is(err, sessErr) {
				err = errors.Join(err, sessErr)
			}
			if status == outcome.Completed || (thinkingStop && status == outcome.Stopped && context.Cause(ctx) == nil) {
				status = outcome.Failed
				reason = "session recording failed: " + sessErr.Error()
			}
		}
		r.Outcome = outcome.Outcome{Status: status, Reason: reason}
		if status == outcome.Completed || status == outcome.Stopped && r.settlement != nil && r.settlement.Qualified {
			contract := "no declared chat check; correctness/test count unverified"
			if r.CodingRequired {
				contract = "declared check passed; coverage/test count unknown"
			}
			if len(r.workflows) > 0 {
				contract = "workflow verification follows owning check/review receipts"
			}
			r.Evidence = fmt.Sprintf("Model-reported response. Bash receipts: %d settled (%d failed), %d blocked, %d incomplete; %s.", settled, failed, blocked, incomplete, contract)
			if status != outcome.Completed {
				r.Evidence = "Run stopped: " + reason + ". Qualified Task evidence retained. " + r.Evidence
			}
			r.Evidence += r.completionEvidence()
			fmt.Fprintln(out, r.Evidence)
			log("evidence", r.Evidence)
			if sessErr != nil && err == nil {
				err = sessErr
				r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: "evidence recording failed: " + sessErr.Error()}
			}
		}
		if status != outcome.Completed {
			n := len(r.Continuation.Entries)
			if n == 0 || r.Continuation.Entries[n-1].Kind != "failure" || !strings.Contains(r.Continuation.Entries[n-1].Message.Content, reason) {
				add("failure", "user", "Harness outcome: "+reason, false)
			}
		}
		if r.Evidence != "" {
			add("evidence", "user", r.Evidence, false)
		}
		if r.CodingRequired {
			r.Continuation.Coding.Closed = r.Outcome.Status == outcome.Completed
		}
		r.Continuation = r.Continuation.Snapshot()
		encoded, snapshotErr := r.Continuation.Encode()
		if snapshotErr == nil {
			log("continuation", encoded)
		} else {
			msg := "continuation recording failed: " + snapshotErr.Error()
			fmt.Fprintln(out, msg)
			r.Continuation.Add("failure", "user", msg, false)
			log("error", msg)
			sessErr = errors.Join(sessErr, snapshotErr)
		}
		if sessErr != nil {
			err = errors.Join(err, sessErr)
			if r.Outcome.Status == outcome.Completed || (thinkingStop && r.Outcome.Status == outcome.Stopped && context.Cause(ctx) == nil) {
				r.Outcome = outcome.Outcome{Status: outcome.Failed, Reason: "continuation recording failed: " + sessErr.Error()}
			}
		}
		if r.CodingRequired && r.Outcome.Status != outcome.Completed {
			r.Continuation.Coding.Closed = false
		}
		return text, transcript, err
	}
	deliverBtw := func() error {
		if r.Btw == nil {
			return nil
		}
		for note := r.Btw(); note != ""; note = r.Btw() {
			log("btw", note)
			if sessErr != nil {
				return sessErr
			}
			if r.assignedClaim && r.CommentAppend != nil {
				if err := r.CommentAppend(ctx, note); err != nil {
					add("note", "user", "Undelivered user comment: "+note, false)
					return err
				}
			}
			add("note", "user", "USER COMMENT: "+note, false)
			if r.assignedClaim {
				r.Continuation.Entries[len(r.Continuation.Entries)-1].Round = session.RoundFor(r.Continuation.Assignment, "")
			}
			r.Continuation.Invalidate()
			contextEvent(r.Continuation)
		}
		return sessErr
	}
	deferredBoundaryAdvice := ""
	deferCompaction := false // A declined length-boundary summary must still yield a worker turn.
	var pruning pruneBatch
	compactModel := func(candidate session.Continuation) (session.Continuation, string, bool, error) {
		requestCtx, cancel := context.WithCancelCause(ctx)
		if r.GenerationInterrupt != nil {
			r.GenerationInterrupt(func() { cancel(userCommentInterrupt) })
		}
		next, summary, ran, err := r.Compact(requestCtx, candidate)
		if r.GenerationInterrupt != nil {
			r.GenerationInterrupt(nil)
		}
		cause := context.Cause(requestCtx)
		cancel(nil)
		if errors.Is(cause, userCommentInterrupt) {
			err = errors.Join(err, userCommentInterrupt)
		}
		return next, summary, ran, err
	}
	publishCompact := func(next session.Continuation, summary string) error {
		encoded, err := next.Snapshot().Encode()
		if err != nil {
			return err
		}
		log("compact", summary)
		log("continuation", encoded)
		contextEvent(next)
		if sessErr != nil {
			return sessErr
		}
		r.Continuation = next
		transcript = next.Messages()
		return nil
	}
steps:
	for step := 1; r.withinStepBudget(step - 1); step++ {
		usedSteps = step
		if err := ctx.Err(); err != nil {
			return finish(outcome.Cancelled, err.Error(), "", err)
		}

		if err := r.archiveClaimPreparation(ctx); err != nil {
			return finish(outcome.Failed, err.Error(), "", err)
		}
		normalFinal = false
		if err := deliverBtw(); err != nil {
			return finish(outcome.Failed, err.Error(), "", err)
		}
		if deferredBoundaryAdvice != "" {
			add("guidance", "user", deferredBoundaryAdvice, false)
			deferredBoundaryAdvice = ""
		}
		r.progressCheckpoint(usedSteps, startedAt)
		if repair, e := r.enforce(ctx, false, "", func(k, t string) error { log(k, t); return sessErr }); e != nil {
			return finish(outcome.Failed, e.Error(), "", e)
		} else if e := decisionGuidance(repair); e != nil {
			return finish(outcome.Failed, e.Error(), "", e)
		}
		if r.Assigned != nil && sessErr != nil {
			return finish(outcome.Failed, sessErr.Error(), "", sessErr)
		}

		if r.TaskComments != nil {
			comments, err := r.TaskComments(ctx)
			if err != nil {
				return finish(outcome.Failed, err.Error(), "", err)
			}
			for _, comment := range comments {
				raw, err := json.Marshal(comment)
				if err != nil {
					return finish(outcome.Failed, err.Error(), "", err)
				}
				text := r.Prompts.Text("continuation.task_comment_prefix") + string(raw)
				delivered := false
				for _, entry := range r.Continuation.Entries {
					if entry.Kind == "note" && entry.Message.Content == text {
						delivered = true
					}
				}
				if delivered {
					continue
				}
				if r.CommentReceipt != nil {
					err = r.CommentReceipt(ctx, comment)
				} else if r.Session == nil {
					err = fmt.Errorf("task comment delivery needs a durable sink")
				} else {
					log("task_comment", string(raw))
					err = sessErr
				}
				if err != nil {
					return finish(outcome.Failed, err.Error(), "", err)
				}
				add("note", "user", text, false)
				if a := r.Continuation.Assignment; r.assignedClaim && a != nil && a.TaskID == comment.TaskID && a.Attempt == comment.Attempt {
					r.Continuation.Entries[len(r.Continuation.Entries)-1].Round = session.RoundFor(a, comment.ID)
				}
				r.Continuation.Invalidate()
				contextEvent(r.Continuation)
			}
		}
		if err := r.refreshWorkflow(); err != nil {
			return finish(outcome.Failed, err.Error(), "", err)
		}
		defs, definitionErr = r.definitions()
		if definitionErr != nil {
			return finish(outcome.Failed, definitionErr.Error(), "", definitionErr)
		}
		if changed, err := r.maintainDiscovery(ctx); err != nil {
			return finish(outcome.Stopped, "active context maintenance: "+err.Error(), "", err)
		} else if changed {
			contextEvent(r.Continuation)
		}
		revision := r.Continuation.Revision
		basis, report := pruning.apply(r, &r.Continuation)
		transcript = r.Continuation.Messages()
		if report != "" {
			kind := "pressure"
			if revision != r.Continuation.Revision {
				kind = "prune"
			}
			log(kind, report)
			if revision != r.Continuation.Revision {
				contextEvent(r.Continuation)
			}
		}
		if !deferCompaction && shouldCompact(r.contextOccupancy(basis), r.MaxTokens, true, r.Compaction) {
			nt, sum, ran, err := compactModel(r.Continuation)
			if err != nil {
				if errors.Is(err, userCommentInterrupt) && ctx.Err() == nil {
					step--
					continue steps
				}
				msg := fmt.Sprintf("stopped: %v", err)
				fmt.Fprintln(out, msg)
				if errors.Is(err, compactionRecordFailure) {
					return finish(outcome.Failed, msg, msg, errors.Join(err, sessErr))
				}
				return finish(outcome.Stopped, msg, msg, sessErr)
			}
			if ran {
				if err := publishCompact(nt, sum); err != nil {
					return finish(outcome.Failed, "compacted continuation recording failed: "+err.Error(), "", err)
				}
				basis = nt.Estimate()
				note := fmt.Sprintf("compacted: content estimate %d tokens (%d%% of window %d), summary #%d",
					basis, basis*100/r.MaxTokens, r.MaxTokens, r.Continuation.Compactions)
				if report != "" {
					note += " (after pruning: " + report + ")"
				}
				fmt.Fprintln(out, note)
			}
		}
		deferCompaction = false
		if overBudget(basis, r.MaxTokens, true) {
			msg := fmt.Sprintf("stopped: content estimate %d tokens reached budget %d (backend token occupancy unmeasured) — the task needs a bigger window or breaking down", basis, r.MaxTokens)
			fmt.Fprintln(out, msg)
			return finish(outcome.Stopped, msg, msg, sessErr)
		}
		requestChars := contentSize(transcript) + r.Continuation.SchemaBytes
		var native llm.Reply
		var reply string
		var u llm.Usage
		var err error
		continuationFailure := ""
		if m, ok := r.Model.(ToolModel); ok {
			var prefix *llm.Reply
			for {
				requestCtx, cancelGeneration := context.WithCancelCause(ctx)
				if prefix != nil {
					requestCtx = llm.WithResponseContinuation(requestCtx, *prefix)
					requestChars = contentSize(transcript) + r.Continuation.SchemaBytes
					for _, raw := range prefix.Prefix.RawChunks {
						requestChars += len(raw)
					}
				}
				if r.GenerationInterrupt != nil {
					r.GenerationInterrupt(func() { cancelGeneration(userCommentInterrupt) })
				}
				observedCtx, setupErr := r.thinkingContext(requestCtx, func(kind, text string) error { log(kind, text); return sessErr }, func(advice string) { deferredBoundaryAdvice = advice })
				if setupErr == nil {
					native, err = m.ChatTools(observedCtx, transcript, defs, r.OnToken)
				} else {
					err = setupErr
				}
				if r.GenerationInterrupt != nil {
					r.GenerationInterrupt(nil)
				}
				cause := context.Cause(requestCtx)
				cancelGeneration(nil)
				if errors.Is(cause, userCommentInterrupt) && ctx.Err() == nil {
					r.recordUsage(native.Usage)
					if err := r.keepSteered(native, func(kind, text string) error { log(kind, text); return sessErr }); err != nil {
						return finish(outcome.Failed, err.Error(), "", err)
					}
					if err := deliverBtw(); err != nil {
						return finish(outcome.Failed, err.Error(), "", err)
					}
					step-- // A discarded generation is not another tool/effect step.
					continue steps
				}
				if !native.OutputLimit || err == nil || ctx.Err() != nil {
					break
				}
				// A length terminal reports this request's prompt even when the
				// unfinished response must continue or cannot retain a prefix.
				measurePrompt(native.Usage)
				if native.Prefix == nil {
					continuationFailure = "unfinished response cannot continue: runtime did not return literal token-prefix state; use the owned MLX worker with continuation support"
					break
				}
				r.recordUsage(native.Usage)
				if _, archiveErr := r.keepPartial(native, err, func(kind, text string) error { log(kind, text); return sessErr }); archiveErr != nil {
					return finish(outcome.Failed, archiveErr.Error(), "", archiveErr)
				}
				r.progressCheckpoint(step-1, startedAt)
				// A length terminal burned a full output budget with no settled
				// receipt: count it toward the consecutive-unproductive-turn
				// stuck trigger so repeated thinking burns get judged advice.
				if watch != nil {
					watch.consecutiveFailures++
				}
				advice, judgeErr := r.reviewDiscovery(ctx, watch, func(kind, text string) error { log(kind, text); return sessErr }, native.Message)
				if judgeErr != nil {
					return finish(outcome.Failed, judgeErr.Error(), "", judgeErr)
				}
				if advice != "" {
					deferredBoundaryAdvice = advice // Delivered only at the next fresh response boundary.
					log("note", "response-limit progress advice recorded; delivery waits for a fresh response or context compaction; current unfinished prefix is unchanged")
				}
				nextInput := basis + len(native.Prefix.Tokens)
				if native.Usage.Reported {
					nextInput = native.Usage.In + native.Usage.Out
				}
				if shouldCompact(nextInput, r.MaxTokens, true, r.Compaction) {
					// A genuine length terminal proves this prompt was processed. The
					// unfinished proposal becomes unverified history, never active calls.
					for index := range r.Continuation.Entries {
						r.Continuation.Entries[index].Unread = false
					}
					if keepErr := r.retainUnfinished(native, func(k, v string) error { log(k, v); return sessErr }); keepErr != nil {
						return finish(outcome.Failed, keepErr.Error(), "", keepErr)
					}
					r.Continuation.BasisTokens, r.Continuation.BasisChars = nextInput, contentSize(r.Continuation.Messages())+r.Continuation.SchemaBytes
					r.Continuation.Measured = false // Approximate projection of the measured input plus generated prefix.
					nt, sum, ran, compactErr := compactModel(r.Continuation)
					if errors.Is(compactErr, userCommentInterrupt) && ctx.Err() == nil {
						step--
						continue steps
					}
					if compactErr != nil {
						status := outcome.Stopped
						if errors.Is(compactErr, compactionRecordFailure) {
							status = outcome.Failed
						}
						return finish(status, "unfinished-response context compaction: "+compactErr.Error(), "", compactErr)
					}
					if !ran {
						// Nothing was applied (ineligible or unapproved summary). That is maintenance
						// information, not a dead end: unless the retained draft
						// genuinely reaches the window, start a fresh response
						// from it (deferred boundary advice delivers there).
						if overBudget(nextInput, r.MaxTokens, true) {
							return finish(outcome.Stopped, "unfinished response reached context cleanup threshold but no model-written compaction could be published; raw draft retained", "", nil)
						}
						log("note", "unfinished response retained as unverified history without compaction; starting a fresh response")
						deferCompaction = true
						step-- // Response handover grants no new execution step.
						continue steps
					}
					if publishErr := publishCompact(nt, sum); publishErr != nil {
						return finish(outcome.Failed, publishErr.Error(), "", publishErr)
					}
					log("note", "unfinished response compacted as unverified history; starting a fresh response from the factual summary")
					step-- // Compaction/response handover grants no new execution step.
					continue steps
				}
				if overBudget(nextInput, r.MaxTokens, true) {
					return finish(outcome.Stopped, "unfinished literal response prefix reaches the configured context window; raw response retained; explicit context cleanup/resume required", "", nil)
				}
				copy := native
				prefix = &copy
				log("note", "output limit reached; continuing the same unfinished native response prefix")
				if sessErr != nil {
					return finish(outcome.Failed, sessErr.Error(), "", sessErr)
				}
			}
			reply = native.Message.Content
			u = native.Usage
		} else {
			reply, _, u, err = r.chat(ctx, transcript)
		}
		var framing *llm.ToolFramingError
		if errors.As(err, &framing) && ctx.Err() == nil && !native.OutputLimit {
			fixed, selected, recoveryErr := r.recoverFraming(ctx, native, framing, func(k, v string) error { log(k, v); return sessErr })
			if recoveryErr != nil {
				if ctx.Err() != nil {
					return finish(outcome.Cancelled, ctx.Err().Error(), "", ctx.Err())
				}
				return finish(outcome.Failed, recoveryErr.Error(), "", recoveryErr)
			}
			if selected {
				native, err = fixed, nil
			} else {
				// Keep the worker's actual reasoning, but no active calls from a
				// refused array. The exact raw reply is in tool-framing-failure.
				retained := native.Message.Clone()
				retained.ToolCalls = nil
				r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "reply", Message: retained})
				feedback := r.Prompts.Format("repair.framing.unrepaired", framing.Reason, framing.Draft)
				log("tool-framing-feedback", feedback)
				add("failure", "user", feedback, false)
				if sessErr != nil {
					return finish(outcome.Failed, sessErr.Error(), "", sessErr)
				}
				r.recordUsage(u)
				measurePrompt(u)
				if watch != nil {
					if trip := watch.observe("tool-framing:"+framing.Draft, false, r.LoopRepeat, r.LoopStall); trip != "" {
						r.progressCheckpoint(step, startedAt)
						advice, adviceErr := r.confirmLoop(ctx, watch, trip, func(k, v string) error { log(k, v); return sessErr }, feedback)
						if adviceErr != nil {
							return finish(outcome.Failed, adviceErr.Error(), "", adviceErr)
						}
						if advice != "" {
							add("guidance", "user", advice, false)
						}
						watch.reset()
					}
				}
				transcript = r.Continuation.Messages()
				continue
			}
		}
		if err != nil {
			if !native.OutputLimit || ctx.Err() != nil {
				r.Continuation.Measured = false
				contextEvent(r.Continuation)
			}
			if u.Reported {
				r.UsedIn += u.In
				r.UsedOut += u.Out
			} else {
				r.UsageUnknown = true
			}
			partial := ""
			var archiveErr error
			if _, isNative := r.Model.(ToolModel); isNative {
				var note string
				note, archiveErr = r.keepPartial(native, err, func(kind, text string) error { log(kind, text); return sessErr })
				partial = "partial reply (not complete; not executed):\n" + note
				log("partial", partial)
				if reply != "" {
					partial = "partial reply (not complete; not executed):\n" + reply + "\n" + note
				}
				fmt.Fprintln(out, partial)
				err = errors.Join(err, archiveErr)
			} else if reply != "" {
				partial = "partial reply (not complete; not executed):\n" + reply
				fmt.Fprintln(out, partial)
				add("reply", "assistant", partial, false)
				log("partial", partial)
			}
			if continuationFailure != "" {
				err = errors.Join(err, errors.New(continuationFailure))
			}
			failure := "model error: " + err.Error()
			if u.Reported {
				failure += fmt.Sprintf("\npartial response usage: in=%d out=%d (server-reported)", u.In, u.Out)
			} else {
				failure += "\npartial response usage: unknown (not reported by server)"
			}
			fmt.Fprintln(out, failure)
			add("failure", "user", failure, false)
			log("error", failure)
			if sessErr != nil {
				return finish(outcome.Failed, failure, partial, fmt.Errorf("model failure: %w; session recording also failed: %w", err, sessErr))
			}
			if errors.Is(err, thinkingLoopStop) && archiveErr == nil {
				thinkingStop = true
				return finish(outcome.Stopped, err.Error(), partial, nil)
			}
			return finish(outcome.Failed, failure, partial, err)
		}
		if u.Reported {
			r.UsedIn += u.In
			r.UsedOut += u.Out
			r.Continuation.BasisTokens, r.Continuation.BasisChars = u.In, requestChars
		} else {
			r.UsageUnknown = true
		}
		measurePrompt(u)
		for i := range r.Continuation.Entries {
			r.Continuation.Entries[i].Unread = false
		}
		if native.ThinkingOnly && native.FinishReason == "stop" && strings.TrimSpace(reply) == "" && len(native.Message.ToolCalls) == 0 &&
			(native.Message.Thinking != "" || native.Message.Reasoning != "" || native.Message.ReasoningContent != "") {
			if keepErr := r.keepThinking(native, func(kind, text string) error { log(kind, text); return sessErr }); keepErr != nil {
				return finish(outcome.Failed, keepErr.Error(), "", keepErr)
			}
			log("note", "model completed thinking only; continuing from retained context")
			if sessErr != nil {
				return finish(outcome.Failed, sessErr.Error(), "", sessErr)
			}
			transcript = r.Continuation.Messages()
			continue
		}
		if len(native.Message.ToolCalls) > 0 {
			if watch != nil {
				watch.completedDiscovery(native.Message, r.LoopStall)
			}
			trip, refused := "", ""
			observe := func(sig string, productive bool, refusal string) string {
				if watch != nil {
					if r.enforcement.current != nil {
						watch.discoveryReceipt(r.enforcement.current, r.LoopStall)
					}
					if desc := watch.observe(sig, r.bookkeepingProgress(ctx, productive), r.LoopRepeat, r.LoopStall); desc != "" {
						trip, refused = desc, refusal
					}
				}
				return "" // Complete the correlated array before progress judgment.
			}
			obs, stopped, nativeErr := r.nativeStep(ctx, native.Message, exec, func(kind, text string) error { log(kind, text); return sessErr }, r.remainingSteps(step-1), observe)
			step += r.nativeCharges - 1
			usedSteps = step
			transcript = r.Continuation.Messages()
			if nativeErr != nil {
				raw, _ := json.Marshal(native.Message)
				log("partial", string(raw))
				return finish(outcome.Failed, nativeErr.Error(), string(raw), nativeErr)
			}
			if ctx.Err() != nil {
				return finish(outcome.Cancelled, ctx.Err().Error(), obs, ctx.Err())
			}
			if stopped != "" {
				return finish(outcome.Stopped, stopped, stopped, nil)
			}
			if trip != "" {
				r.progressCheckpoint(usedSteps, startedAt)
				advice, err := r.confirmLoop(ctx, watch, trip, func(k, t string) error { log(k, t); return sessErr }, refused)
				if err != nil {
					return finish(outcome.Failed, err.Error(), obs, err)
				}
				if advice != "" {
					add("guidance", "user", advice, false)
				}
				watch.reset()
				if r.LoopStall > 0 {
					watch.discoveryCalls %= r.LoopStall
				}
			}
			if err := reviewDiscovery(); err != nil {
				return finish(outcome.Failed, err.Error(), obs, err)
			}
			continue
		}
		fmt.Fprintln(out, reply)
		add("reply", "assistant", reply, false)
		if _, ok := r.Model.(ToolModel); ok {
			if err := r.keepCompleted(native, func(kind, text string) error { log(kind, text); return sessErr }); err != nil {
				r.Continuation.Entries = r.Continuation.Entries[:len(r.Continuation.Entries)-1]
				return finish(outcome.Failed, err.Error(), reply, err)
			}
			transcript = r.Continuation.Messages()
		} else {
			log("reply", reply)
		}
		if err := ctx.Err(); err != nil {
			return finish(outcome.Cancelled, err.Error(), reply, err)
		}
		if watch != nil {
			message := llm.Message{Role: "assistant", Content: reply}
			if _, ok := r.Model.(ToolModel); ok {
				message = native.Message
			}
			watch.completedDiscovery(message, r.LoopStall)
		}
		call, found, repair := tool.Extract(reply, r.Prompts)
		if structured(r.Model) && found {
			call = tool.Call{}
			repair = r.Prompts.Text("repair.native.text_syntax")
		}
		if !found {
			if strings.TrimSpace(reply) == "" {
				return finish(outcome.Stopped, "model returned no final answer", reply, sessErr)
			}
			normalFinal = true
			previousSettlement := lastSettlementReceipt
			text, msgs, e := finish(outcome.Completed, "", reply, sessErr)
			if errors.Is(e, repairFinal) {
				if watch != nil {
					if lastSettlementReceipt != previousSettlement && r.settlement != nil && r.settlement.Qualified {
						watch.reset() // A newly qualified owning handoff is actual progress.
					} else if desc := watch.observe("", r.bookkeepingProgress(ctx, false), r.LoopRepeat, r.LoopStall); desc != "" {
						advice, err := r.confirmLoop(ctx, watch, desc, func(k, t string) error { log(k, t); return sessErr }, "")
						if err != nil {
							return finish(outcome.Failed, err.Error(), text, err)
						}
						if advice != "" {
							add("guidance", "user", advice, false)
						}
						watch.reset()
					}
				}
				normalFinal = false // A new response has not yet completed; service/stream failures cannot request final repair.
				step = usedSteps
				continue
			}
			return text, msgs, e
		}
		if call.Name != "" {
			log("tool", callText(call))
		}

		obs, sig, productive := r.handleCall(ctx, call, repair, exec)
		log("result", obs)
		kind := "output"
		if def, ok := r.Registry.ResolveName(call.Name); ok && repair == "" && len(call.Slots) == 0 && len(def.Params) > 0 && !boundTaskOmission(def) && !def.Opening {
			_ = def
			kind = "schema"
		} else if !productive {
			kind = "failure"
		}
		add(kind, "user", obs, true)
		if err := r.settleFocus(func(kind, text string) error { log(kind, text); return sessErr }); err != nil {
			return finish(outcome.Failed, err.Error(), obs, err)
		}
		if err := ctx.Err(); err != nil {
			return finish(outcome.Cancelled, err.Error(), obs, err)
		}
		if sessErr == nil && kind != "schema" {
			if sig == "" && !productive && r.enforcement.current == nil {
				r.preparationDisposition = "no calls ran; no IDs were returned by this reply: " + obs
			} else if preparationTool(call.Name) {
				r.preparationDisposition = "Preparation call settled:\n" + r.preparationReceipt(call.Name, "text", obs, sig != "" || r.enforcement.current != nil)
			}
		}
		if watch != nil {
			if r.enforcement.current != nil {
				watch.discoveryReceipt(r.enforcement.current, r.LoopStall)
			}
			if desc := watch.observe(sig, r.bookkeepingProgress(ctx, productive), r.LoopRepeat, r.LoopStall); desc != "" {
				advice, err := r.confirmLoop(ctx, watch, desc, func(k, t string) error { log(k, t); return sessErr }, "")
				if err != nil {
					return finish(outcome.Failed, err.Error(), obs, err)
				}
				if advice != "" {
					add("guidance", "user", advice, false)
				}
				watch.reset()
				if r.LoopStall > 0 {
					watch.discoveryCalls %= r.LoopStall
				}
			}
		}
		if err := reviewDiscovery(); err != nil {
			return finish(outcome.Failed, err.Error(), obs, err)
		}
	}
	msg := fmt.Sprintf("stopped after %d steps without a final answer — the task may need breaking down", r.MaxSteps)
	fmt.Fprintln(out, msg)
	return finish(outcome.Stopped, msg, msg, sessErr)
}

// chat runs one exchange, streaming when OnToken is set and the
// model can stream, plain Chat otherwise.
func (r *Runner) chat(ctx context.Context, transcript []llm.Message) (string, string, llm.Usage, error) {
	if r.OnToken != nil {
		if s, ok := r.Model.(Streamer); ok {
			return s.ChatStream(ctx, transcript, r.OnToken)
		}
	}
	return r.Model.Chat(ctx, transcript)
}

// overBudget reports whether the server's last reported prompt size
// reached MaxTokens. Unknown (have=false) never trips: without a
// measurement there is nothing to judge.
func overBudget(lastPrompt, maxTokens int, have bool) bool {
	return have && maxTokens > 0 && lastPrompt >= maxTokens
}

// callText renders a tool call deterministically for the session record.
func callText(call tool.Call) string {
	keys := make([]string, 0, len(call.Slots))
	for k := range call.Slots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(call.Name)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%s", k, call.Slots[k])
	}
	return b.String()
}

// handleCall runs one parsed call. It returns the observation plus the
// executed-call signature ("" unless the tool ran) and whether the step
// was productive (ran without error, unblocked, and unfailed).
func (r *Runner) handleCall(ctx context.Context, call tool.Call, repair string, exec Exec) (string, string, bool) {
	r.enforcement.current = nil
	if repair != "" {
		return repair, "", false
	}
	return r.handleInvocation(ctx, call, repair, "", exec)
}

// Only the explicitly bound typed task calls execute an omitted text argument.
func boundTaskOmission(def tool.Definition) bool {
	return def.Bind != nil && (def.Name == "ready_task" || def.Name == "task_start")
}

func (r *Runner) handleInvocation(ctx context.Context, call tool.Call, repair, nativeJSON string, exec Exec) (string, string, bool) {
	if repair != "" {
		return repair, "", false
	}
	log := func(kind, text string) error {
		if r.Session != nil {
			return r.Session.Append(ctx, r.SessionID, kind, text)
		}
		return nil
	}
	authored := tool.Args{}
	for name, value := range call.Slots {
		authored[name] = value
	}
	if nativeJSON != "" {
		parsed, err := tool.JSONObject(nativeJSON)
		if err != nil {
			return err.Error(), "", false
		}
		authored = tool.Args(parsed)
	}
	def, ok := r.Registry.ResolveName(call.Name)
	var args tool.Args
	q, note := "", ""
	if !ok {
		q = r.Prompts.Format("repair.unknown_tool", call.Name, strings.Join(r.Registry.Names(), ", "))
	} else {
		if len(call.Slots) == 0 && nativeJSON == "" && len(def.Params) > 0 && !boundTaskOmission(def) && !def.Opening {
			return r.Prompts.Format("repair.schema_only", def.Name, def.Schema()), "", false
		}
		input, discarded := tool.NormalizeOpening(def, authored)
		if nativeJSON != "" {
			raw, err := json.Marshal(input)
			if err != nil {
				return err.Error(), "", false
			}
			args, q = tool.ValidateJSON(def, string(raw))
		} else {
			slots := map[string]string{}
			for name, value := range input {
				slots[name] = value.(string)
			}
			args, q = tool.Validate(def, slots)
		}
		if q == "" && len(discarded) > 0 {
			note = r.Prompts.Format("repair.opening.receipt", strings.Join(discarded, ", "))
		}
	}
	if q != "" {
		fixed, values, selected, err := r.repairInvocation(ctx, call.Name, authored, q, log)
		if err != nil {
			return "call not executed: " + err.Error(), "", false
		}
		if !selected {
			return q, "", false
		}
		def, args = fixed, values
		note = r.Prompts.Format("repair.call_reconstructed.receipt", def.Name)
	}
	def, args, q = prepareInvocation(ctx, def, args)
	if q != "" {
		return q, "", false
	}
	if r.Session != nil {
		receipt, err := callResolution("", call, nativeJSON, def, args)
		if err == nil {
			err = r.Session.Append(ctx, r.SessionID, "call_resolution", receipt)
		}
		if err != nil {
			return "call not executed: normalized invocation recording failed: " + err.Error(), "", false
		}
	}
	text, sig, productive := r.invokeValidated(ctx, def, args, exec)
	return annotateInvocationResult(text, note), sig, productive
}

// Keep structured tool receipts parseable; existing data/effects remain unchanged.
func annotateInvocationResult(text, note string) string {
	if note == "" {
		return text
	}
	const prefix = "tool result:\n"
	if strings.HasPrefix(text, prefix) {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(strings.TrimPrefix(text, prefix)), &object) == nil && object != nil {
			// A result owner may already use this name; never overwrite its data.
			key := "_harness_call_note"
			for n := 2; ; n++ {
				if _, exists := object[key]; !exists {
					break
				}
				key = fmt.Sprintf("_harness_call_note_%d", n)
			}
			object[key], _ = json.Marshal(note)
			if raw, err := json.Marshal(object); err == nil {
				return prefix + string(raw)
			}
		}
	}
	return text + "\n" + note
}

// Original call bytes stay in the reply archive; this receipt records the exact
// validated action sent to authorization, including bound scope/path expansion.
func callResolution(id string, call tool.Call, nativeJSON string, def tool.Definition, args tool.Args) (string, error) {
	var original any = call.Slots
	if nativeJSON != "" {
		original = nativeJSON // Keep the original JSON spelling as a string, byte-exact.
	}
	raw, err := json.Marshal(struct {
		ToolCallID        string
		OriginalName      string
		OriginalArguments any
		Name              string
		Arguments         tool.Args
	}{id, call.Name, original, def.Name, args})
	return string(raw), err
}

// prepareInvocation is shared by text and whole-array native preflight.
func prepareInvocation(ctx context.Context, def tool.Definition, args tool.Args) (tool.Definition, tool.Args, string) {
	if def.NormalizeArgs != nil {
		normalized, err := def.NormalizeArgs(ctx, args)
		if err != nil {
			return def, nil, "scope refused: " + err.Error()
		}
		raw, err := json.Marshal(normalized)
		if err != nil {
			return def, nil, err.Error()
		}
		var repair string
		args, repair = tool.ValidateJSON(def, string(raw))
		if repair != "" {
			return def, nil, repair
		}
	}
	if def.Bind == nil {
		return def, args, ""
	}
	original, expanded, err := def.Bind(ctx, args)
	if err != nil {
		return original, nil, "scope refused: " + err.Error()
	}
	raw, err := json.Marshal(expanded)
	if err != nil {
		return original, nil, err.Error()
	}
	validated, repair := tool.ValidateJSON(original, string(raw))
	original.Bound = true
	return original, validated, repair
}
func (r *Runner) settleFocus(log func(string, string) error) error {
	if r.pendingFocus == nil {
		return nil
	}
	defer func() { r.pendingFocus = nil }()
	if r.Session == nil {
		return fmt.Errorf("workflow focus requires a durable continuation receipt")
	}
	candidate := r.Continuation.Clone()
	value := *r.pendingFocus
	candidate.Workflow = &value
	// A focus change keeps the model and conversation, so the measured
	// chars-per-token calibration stays valid; a model change still clears
	// it on restore. Clearing it here made pruning count bytes as tokens.
	candidate.Invalidate()
	raw, err := candidate.Snapshot().Encode()
	if err == nil {
		err = log("continuation", raw)
	}
	if err == nil {
		r.Continuation = candidate
		raw, _ := json.Marshal(session.Continuation{ModelKey: candidate.ModelKey})
		err = log("context", string(raw))
	}
	return err
}

// invokeValidated shares owning resolution and execution with both protocols.
func (r *Runner) invokeValidated(ctx context.Context, def tool.Definition, args tool.Args, exec Exec) (string, string, bool) {
	ref := ""
	if def.WorkflowRefArg != "" {
		ref, _ = args[def.WorkflowRefArg].(string)
		var err error
		if def.ResolveWorkflowRef != nil {
			ref, err = def.ResolveWorkflowRef(ctx, args)
		} else {
			err = fmt.Errorf("workflow reference resolver is not wired")
		}
		if ref == "" && err == nil {
			err = fmt.Errorf("workflow reference resolver returned no owning reference")
		}
		if ref != "" {
			if r.workflows == nil {
				r.workflows = map[string]outcome.Report{}
				r.Evidence = ""
			}
			reason := "workflow invocation has not qualified"
			if err != nil {
				reason = err.Error()
			}
			r.workflows[ref] = outcome.Report{Ref: ref, Outcome: outcome.Outcome{Status: outcome.Failed, Reason: reason}}
		}
		if err != nil {
			return "workflow reference refused: " + err.Error(), "", false
		}
	}
	if err := ctx.Err(); err != nil {
		return "tool cancelled: " + err.Error(), "", false
	}
	res, err := exec(ctx, def, args)
	if ref != "" && res.Workflow != nil && res.Workflow.Ref == ref && err == nil && !res.Blocked && (!res.Workflow.Qualified || !res.Failed && res.Workflow.Outcome.Status == outcome.Completed) {
		r.workflows[ref] = *res.Workflow
	}
	if ref != "" && (err != nil || res.Blocked) {
		report := r.workflows[ref]
		if err != nil {
			report.Outcome.Reason = err.Error()
		} else {
			report.Outcome.Reason = res.Text
		}
		r.workflows[ref] = report
	}
	if err != nil {
		obs := "tool error: " + err.Error()
		if res.Text != "" {
			obs += "\npartial tool result (failed; not complete):\n" + res.Text
		}
		return obs, callSignature(def.Name, args), false
	}
	if res.Blocked {
		return "tool result:\n" + res.Text, "", false
	}
	if res.Failed {
		return "tool result:\n" + res.Text, callSignature(def.Name, args), false
	}
	note := ""
	if res.Focus != nil {
		value := *res.Focus
		if a := r.Continuation.Assignment; a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) && !r.ClaimedTaskScope(res.Focus) {
			// Browsing/metadata can return another node, but cannot hand off an
			// unfinished claim or strand its check/review behind another tool view.
			note = r.Prompts.Format("continuation.workflow.active_claim_focus", value.Kind, value.ID, a.TaskID)
			if def.Name == "workflow_focus" {
				res.Text = strings.Replace(res.Text, "workflow focus for next request:", "requested workflow target:", 1)
			}
			value = session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}
		}
		r.pendingFocus = &value
	}
	if res.Truncated {
		return annotateInvocationResult("tool result (truncated):\n"+res.Text, note), callSignature(def.Name, args), true
	}
	return annotateInvocationResult("tool result:\n"+res.Text, note), callSignature(def.Name, args), true
}

func systemPrompt(reg *tool.Registry) string {
	var b strings.Builder
	b.WriteString(reg.Prompts.Text("system.text.instructions"))
	b.WriteString("Available tools:\n")
	for _, n := range reg.Names() {
		if d, ok := reg.Lookup(n); ok {
			fmt.Fprintf(&b, "- %s: %s\n", n, d.Desc)
		}
	}
	return b.String()
}

// Publish cleanup only after its exact candidate is validated and durably saved.
func (r *Runner) retireTaskGuidance(ctx context.Context, round *session.TaskRound) error {
	candidate := r.Continuation.Clone()
	if !candidate.RetireGuidance(round) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := candidate.Snapshot().Encode()
	if err != nil {
		return err
	}
	if r.Session == nil {
		return fmt.Errorf("task-round cleanup needs a durable session")
	}
	if err = r.Session.Append(ctx, r.SessionID, "continuation", raw); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r.Continuation = candidate
	return nil
}

// Zero is the explicit absence of a call ceiling; positive limits retain existing charges.
func (r *Runner) withinStepBudget(used int) bool { return r.MaxSteps == 0 || used < r.MaxSteps }
func (r *Runner) remainingSteps(used int) *int {
	if r.MaxSteps == 0 {
		return nil
	}
	n := max(0, r.MaxSteps-used)
	return &n
}
func (r *Runner) remainingRunSeconds(elapsed int64) *int {
	if r.MaxRunSeconds == 0 {
		return nil
	}
	n := max(0, r.MaxRunSeconds-int(elapsed))
	return &n
}
