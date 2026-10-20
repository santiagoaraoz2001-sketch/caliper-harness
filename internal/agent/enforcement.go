package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"time"
)

// AssignedOps connects the single main loop to its owning PAT, without imports back to launch.
type AssignedOps struct {
	Store             string // owning identity for preparation advice, never assignment authority
	ConfiguredCommand string
	AcceptanceCommand func() string
	ClaimedBrief      func() map[string]string
	Observe           func(tool.Definition, tool.Args, tool.Result, error)
	Reset             func()
	Start             func(context.Context, string) (*session.Assignment, string, error)
	Admit             func(context.Context) error
	ProjectCoding     func(context.Context) error // ordinary project work; never a Task claim
	Facts             func(context.Context) (any, error)
	Settle            func(context.Context, outcome.Outcome, string, string, string, []llm.Message, *session.CodingState) (TaskSettlement, error)
}

// TaskSettlement is an owning receipt, not a model verdict or renewed claim.
type TaskSettlement struct {
	TaskID, State, Feedback string
	Attempt                 int
	Check, Verification     outcome.Verification
	Qualified               bool
}

func (s TaskSettlement) rejected() bool {
	return !s.Qualified && (s.Check == outcome.Rejected || s.Verification == outcome.Rejected)
}

// BookkeepingFacts preserves bounded owning evidence; Preparation is advice, never authority.
type BookkeepingFacts struct {
	Assignment  any
	Unfinished  json.RawMessage
	Knowledge   []map[string]string
	Disclosure  string
	Preparation string
	Checkpoint  ProgressCheckpoint
}

// ProgressCheckpoint contains current owner state and receipt provenance, never a diagnosis.
type ProgressCheckpoint struct {
	Phase, PATStep, SelectedID, MissingFact string
	RunStep, StepLimit, RunLimitSeconds     int
	ElapsedSeconds                          int64
	RecentSource                            *SourceCheckpoint `json:",omitempty"`
	NextAction                              string
}

type SourceCheckpoint struct{ Tool, Path, Range, FileSHA256, ReceiptSHA256 string }

// progressCheckpoint receives the existing Run's actual clock and charged step.
func (r *Runner) progressCheckpoint(usedSteps int, startedAt time.Time) {
	r.enforcement.runStep = usedSteps
	r.enforcement.elapsedSeconds = int64(time.Since(startedAt).Seconds())
}

func owningFactsDigest(facts any) [32]byte {
	if f, ok := facts.(BookkeepingFacts); ok {
		f.Preparation, f.Checkpoint = "", ProgressCheckpoint{}
		facts = f
	}
	raw, _ := json.Marshal(facts)
	return sha256.Sum256(raw)
}

func (r *Runner) currentProgress(facts any) ProgressCheckpoint {
	p := ProgressCheckpoint{Phase: "PREPARE", PATStep: "unknown", MissingFact: "unknown"}
	if f, ok := facts.(BookkeepingFacts); ok {
		p = f.Checkpoint
		p.NextAction = f.Preparation
	}
	if a := r.Continuation.Assignment; a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) {
		p.Phase, p.PATStep, p.SelectedID = "EXECUTE", "claimed", r.Continuation.Assignment.TaskID
		p.NextAction = r.Prompts.Text("progress.execute")
		if c := r.Continuation.Coding; c != nil && c.Passed() {
			p.Phase, p.PATStep = "VALIDATE", "current-check-observed"
			p.NextAction = r.Prompts.Text("progress.validate")
		}
	}
	if s := r.settlement; s != nil {
		p.Phase, p.PATStep, p.SelectedID = "REWORK", "settled-"+s.State, s.TaskID
		p.NextAction = r.Prompts.Text("progress.rework")
		if s.Qualified {
			p.Phase = "HANDOFF"
			if f, ok := facts.(BookkeepingFacts); ok && f.Preparation != "" {
				p.NextAction = f.Preparation
			} else {
				p.NextAction = r.Prompts.Text("progress.complete")
			}
		}
	}
	if p.Phase == "" {
		p.Phase = "PREPARE"
	}
	if p.PATStep == "" {
		p.PATStep = "unknown"
	}
	if p.MissingFact == "" {
		p.MissingFact = "unknown"
	}
	p.RunStep, p.StepLimit, p.RunLimitSeconds, p.ElapsedSeconds = r.enforcement.runStep, r.MaxSteps, r.MaxRunSeconds, r.enforcement.elapsedSeconds
	if note := r.enforcement.discovery; note != nil {
		s := &SourceCheckpoint{Tool: note.Tool, Range: "unknown", FileSHA256: "unknown", ReceiptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(note.Result.Text)))}
		s.Path, _ = note.Args["path"].(string)
		if note.Error == "" && !note.Result.Failed && !note.Result.Blocked && (note.Tool == "read" || note.Tool == "read_outside") {
			header, _, _ := strings.Cut(note.Result.Text, "\n")
			for _, field := range strings.Fields(header) {
				if value, ok := strings.CutPrefix(field, "sha256="); ok {
					if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == sha256.Size {
						s.FileSHA256 = value
					}
				}
			}
			if index := strings.LastIndex(note.Result.Text, "\n["); index >= 0 {
				footer := note.Result.Text[index:]
				if strings.HasSuffix(footer, "metadata not source lines]") {
					for _, field := range strings.Split(footer, "; ") {
						if value, ok := strings.CutPrefix(strings.TrimPrefix(strings.TrimSpace(field), "["), "shown_range="); ok {
							s.Range = value
						}
					}
				}
			}
		}
		p.RecentSource = s
	}
	return p
}

type observed struct {
	PlanningQuestion string // successful guided draft's next question, not an instruction from the model
	CallID           string
	ObservedAt       time.Time
	Tool             string
	Args             tool.Args
	Result           tool.Result
	Error            string
}
type enforcement struct {
	current               *observed // this call only; cleared before validation/schema-only handling
	latest                *observed
	discovery             *observed
	facts                 [32]byte
	digest                [32]byte
	repair                string
	presentation          string // current model-facing directive; full repair remains judge evidence
	knowledgePresentation string
	prepare               bool              // actual decision requested PAT advice; not execution authority
	knowledge             bool              // preserve unresolved product-knowledge guidance separately
	discovered            map[[32]byte]bool // Per run; bounded by charged tool calls.
	repairFacts           [32]byte
	runStep               int
	elapsedSeconds        int64
}

var repairFinal = fmt.Errorf("bookkeeping repair requested within the same run")

const coherent = "continue with coherent PAT and product knowledge"

var enforceOptions = []string{coherent, "request executor PAT update", "request supported product knowledge update", "request both PAT and product knowledge updates", "request focused correction of conflicting PAT or product knowledge"}

// PlanningActive derives the explicit workflow boundary; historical assignment is not a claim.
func (r *Runner) PlanningActive() bool {
	return r.Continuation.Workflow != nil || r.pendingFocus != nil || r.assignedClaim
}

// PlanningScope preserves explicit navigation. Without it, a current qualified
// claim supplies only the view used for the next owning handoff; restored history
// alone cannot select work, and no focus or completion receipt is manufactured.
func (r *Runner) PlanningScope() *session.WorkflowScope {
	if r.Continuation.Workflow != nil {
		return r.Continuation.Workflow
	}
	a, s := r.Continuation.Assignment, r.settlement
	if r.Assigned != nil && r.assignedClaim && a != nil && s != nil && s.Qualified &&
		a.Store != "" && a.Workspace == r.Workspace && s.TaskID == a.TaskID && s.Attempt == a.Attempt {
		return &session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}
	}
	return nil
}

// ClaimedTaskScope identifies this run's actual matching main claim.
func (r *Runner) ClaimedTaskScope(scope *session.WorkflowScope) bool {
	a, c := r.Continuation.Assignment, r.Continuation.Coding
	return r.Assigned != nil && r.assignedClaim && r.settlement == nil && r.Outcome.Status == outcome.Unknown && r.codingErr == nil &&
		scope != nil && scope.Kind == "task" && a != nil && c != nil &&
		scope.Store == a.Store && scope.ID == a.TaskID && a.Workspace == r.Workspace && a.Attempt > 0 &&
		c.TaskID == a.TaskID && c.Attempt == a.Attempt && c.Workspace == a.Workspace && !c.Closed
}

func (r *Runner) TaskStartDefinition() tool.Definition {
	return tool.Definition{Name: "task_start", Desc: r.Prompts.Text("tools.task_start.description"), Purpose: r.Prompts.Text("tools.task_start.purpose"), Params: []tool.Param{{Name: "task", Type: tool.TypeString, Required: true}}, Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
		if r.Assigned == nil || r.Session == nil {
			return tool.Result{}, fmt.Errorf("task_start needs owning store/project and durable session")
		}
		value, command, err := r.Assigned.Start(ctx, args["task"].(string))
		if err != nil {
			return tool.Result{}, err
		}
		firstClaim := !r.assignedClaim
		r.assignedClaim = true
		r.settlement = nil
		r.Continuation.Assignment = value
		r.Continuation.Coding = &session.CodingState{TaskID: value.TaskID, Attempt: value.Attempt, Workspace: value.Workspace, Command: command, ConfigCommand: r.Assigned.ConfiguredCommand, Source: "assigned"}
		if r.Assigned.AcceptanceCommand != nil {
			r.Continuation.Coding.AcceptanceCommand = r.Assigned.AcceptanceCommand()
		}
		r.CheckCommand = command
		if err = r.codingRecord(ctx); err != nil {
			return tool.Result{}, err
		}
		snapshot := r.Continuation.Snapshot()
		// The durable claim cannot replay an in-flight native call group as settled.
		for i := len(snapshot.Entries) - 1; i >= 0; i-- {
			if calls := len(snapshot.Entries[i].Message.ToolCalls); calls > 0 && i+calls >= len(snapshot.Entries) {
				snapshot.Entries = snapshot.Entries[:i]
				break
			}
		}
		raw, err := snapshot.Encode()
		if err == nil {
			err = r.Session.Append(ctx, r.SessionID, "continuation", raw)
		}
		if err != nil {
			r.codingErr = err
			return tool.Result{}, err
		}
		if firstClaim {
			r.claimPreparationEnd = len(r.Continuation.Entries) - 1
		}
		text := fmt.Sprintf("claimed task %s attempt%d; direct main execution admitted, check %q", value.TaskID, value.Attempt, command)
		// refreshWorkflow installs the frozen brief at system role before the
		// next request. Keep this receipt about the actual claim, not a second brief.
		return tool.Result{Text: text}, nil
	}}
}
func executionTool(name string) bool {
	switch name {
	case "edit", "write", "bash", "check", "launch", "workflow_resume":
		return true
	}
	return false
}

func (r *Runner) codingAdmission(ctx context.Context, d tool.Definition) error {
	if r.Assigned == nil || !r.PlanningActive() || !executionTool(d.Name) {
		return nil
	}
	if r.codingErr != nil {
		return r.codingErr
	}
	if r.assignedClaim {
		return r.Assigned.Admit(ctx)
	}
	if d.Name == "launch" || d.Name == "workflow_resume" {
		return nil // These existing operations own their explicit lifecycle admission.
	}
	if r.settlement != nil || r.Assigned.ProjectCoding == nil {
		return fmt.Errorf("selected work has no ordinary project coding admission; inspect current plan state")
	}
	return r.Assigned.ProjectCoding(ctx)
}

func (r *Runner) assignmentExec(next Exec) Exec {
	return func(ctx context.Context, d tool.Definition, args tool.Args) (result tool.Result, err error) {
		defer func() {
			if r.Assigned != nil && r.Assigned.Observe != nil {
				r.Assigned.Observe(d, args, result, err)
			}
			{
				copied := tool.Args{}
				for k, v := range args {
					copied[k] = v
				}
				note := &observed{Tool: d.Name, Args: copied, Result: result, ObservedAt: time.Now().UTC()}
				if d.Name == "graphql" {
					encoded, _ := json.Marshal(args)
					note.Args = tool.Args{"operation_sha256": fmt.Sprintf("%x", sha256.Sum256(encoded))}
					var envelope struct {
						Effects      json.RawMessage `json:"effects"`
						Errors       []string        `json:"errors"`
						DataOmitted  bool            `json:"data_omitted"`
						NextQuestion string          `json:"next_question"`
					}
					decoded := json.Unmarshal([]byte(result.Text), &envelope) == nil
					if d.Label == "plan" && decoded && len(envelope.Errors) == 0 && !envelope.DataOmitted && err == nil && !result.Failed && !result.Blocked && !result.Truncated {
						note.PlanningQuestion = envelope.NextQuestion
					}
					envelope.NextQuestion = "" // current owner checkpoint carries this once
					summary, _ := json.Marshal(envelope)
					note.Result.Text = fmt.Sprintf("actual GraphQL response sha256=%x; effects/errors/omission=%s; selected owning facts supplied separately", sha256.Sum256([]byte(result.Text)), summary)
				}
				if err != nil {
					note.Error = err.Error()
				}
				r.enforcement.current = note
				if r.Assigned != nil && d.Name != "help" {
					r.enforcement.latest = note
				}
				if d.Name == "read" || d.Name == "read_outside" || d.Name == "find_outside" || d.Name == "find" || d.Name == "find_file" || d.Name == "find_dir" || d.Name == "grep" {
					r.enforcement.discovery = note
				}
			}
		}()
		if r.Assigned != nil && d.Name == "workflow_transition" && args["kind"] == "task" && (args["to"] == "running" || args["to"] == "done") {
			return tool.Result{}, fmt.Errorf("model task execution states are host-owned; use task_start and actual check/review settlement")
		}
		if err := r.codingAdmission(ctx, d); err != nil {
			return tool.Result{}, err
		}

		return next(ctx, d, args)
	}
}

func (r *Runner) enforce(ctx context.Context, final bool, provisional string, log func(string, string) error) (string, error) {
	if r.ManualDecisions {
		return "", nil
	} // Explicit opt-out skips advice, never coding admission.
	if r.Assigned == nil || !r.PlanningActive() || !final && r.enforcement.latest == nil {
		return "", nil
	}
	packet, err := r.progressEvidence(ctx, r.progressWatch)
	if err != nil {
		return "", err
	}
	// Successful drafting already supplies the next question. Successful work
	// under a claim is supervised by the existing progress watcher. Neither
	// needs a second bookkeeping judgment after every call. Finals, failures,
	// changed questions and delivered comments retain this enforcement path.
	// Older unexecuted calls remain evidence, but only the current refusal can
	// override a newer successful receipt.
	if note := r.enforcement.latest; !final && note != nil && note.Error == "" && !note.Result.Failed && !note.Result.Blocked && !note.Result.Truncated && (note.Result.ExitCode == nil || *note.Result.ExitCode == 0) && packet.PreflightRefusal == "" && len(packet.DeliveredUserComments) == 0 && len(packet.DeliveredTaskComments) == 0 {
		guided := !r.assignedClaim && note.PlanningQuestion != "" && note.PlanningQuestion == packet.Checkpoint.NextAction
		if guided || r.assignedClaim && r.settlement == nil {
			return "", nil
		}
	}
	// PAT decisions use owner facts and receipts, not duplicate complete source.
	packet.LatestSourceText = ""
	facts := packet.Facts
	latestDiscovery := r.enforcement.discovery
	same := latestDiscovery != nil && latestDiscovery == r.enforcement.latest
	if same {
		latestDiscovery = nil
	}
	input := struct {
		progressPacket
		LatestDiscoverySameAsLatest bool
		NormalFinal                 bool
		ProvisionalModelAnswer      string
	}{packet, same, final, provisional}
	input.Latest, input.LatestDiscovery = loopObserved(r.enforcement.latest), loopObserved(latestDiscovery)
	input.deduplicateEvidence()
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	cacheInput := input
	cacheInput.PreviousAdvice = ""
	cacheInput.RemainingSteps, cacheInput.RemainingSeconds = nil, nil
	cacheInput.PendingRepair = ""                // Host advice is output, not new owning evidence.
	cacheInput.Checkpoint = ProgressCheckpoint{} // Time/advice alone cannot trigger another judgment.
	cacheRaw, err := json.Marshal(cacheInput)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(cacheRaw)
	if digest == r.enforcement.digest {
		return "", nil // No new judgment or new nudge; retained context stays intact.
	}
	if r.Judge == nil {
		return "", fmt.Errorf("PAT/product enforcement has no selected decision judge")
	}
	req := decision.Request{Context: string(raw), Instructions: r.Prompts.Text("decision.bookkeeping.instructions"), Options: enforceOptions}
	req, v, err := r.progressJudgment(ctx, &input, req, input.progressPacket.reduceForCapacity, log)
	if err != nil {
		var unavailable *progressJudgeError
		if !errors.As(err, &unavailable) {
			return "", fmt.Errorf("PAT/product decision unavailable: %w", err)
		}
		// The enforcer is advisory: an unavailable judgment is recorded and
		// the executor continues unchanged, as on the discovery path.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := log("judgment-error", fmt.Sprintf("source=pat-product-enforcer error=%q", err)); err != nil {
			return "", err
		}
		r.enforcement.digest = digest // The same evidence is not judged again.
		return "", nil
	}
	valid := false
	for _, option := range enforceOptions {
		valid = valid || v.Option == option
	}
	if !valid {
		return "", fmt.Errorf("PAT/product decision returned unsupported option")
	}
	resolution := v.Option
	if v.Confidence < advisoryConfidence {
		resolution = "failed-confidence"
	}
	var saved error
	decision.LogJudgment(func(k, t string) {
		if saved == nil {
			saved = log(k, t)
		}
	}, "pat-product-enforcer", req, v, resolution, r.JudgmentContextChars)
	if saved != nil {
		return "", saved
	}
	r.enforcement.digest = digest
	if v.Confidence < advisoryConfidence {
		return "", nil // A failed advisory judgment cannot change work or publish advice.
	}
	r.enforcement.repair = ""
	r.enforcement.presentation = ""
	r.enforcement.knowledgePresentation = ""
	r.enforcement.prepare = v.Option == enforceOptions[1] || v.Option == enforceOptions[3]
	r.enforcement.knowledge = v.Option == enforceOptions[2] || v.Option == enforceOptions[3]
	if v.Option != coherent {
		request, prefix := v.Option, r.Prompts.Text("continuation.bookkeeping.request_prefix")
		suffix := r.Prompts.Text("continuation.bookkeeping.authority_suffix")
		nextAction := r.Prompts.Text("continuation.bookkeeping.next_action")
		claimed := false
		if a := r.Continuation.Assignment; a != nil && r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID}) {
			claimed = true
			suffix = r.Prompts.Text("continuation.bookkeeping.claimed_authority_suffix")
			if r.enforcement.prepare {
				// The owning phase already names the legal next step. A bare
				// "PAT update" leaves an executor guessing at graph mutations.
				request = r.currentProgress(facts).NextAction
				if r.enforcement.knowledge {
					request += " " + enforceOptions[2] + "."
				}
			}
		}
		r.enforcement.repair = prefix + request + suffix
		r.enforcement.presentation = r.enforcement.repair
		if r.enforcement.knowledge {
			r.enforcement.knowledgePresentation = prefix + enforceOptions[2] + suffix
		}
		if facts, ok := facts.(BookkeepingFacts); ok && r.enforcement.prepare && !claimed {
			r.enforcement.repair += nextAction + facts.Preparation
		}
		r.enforcement.repairFacts = owningFactsDigest(facts)
	}
	return r.enforcement.repair, nil
}

// Pending obligations do not erase other observed progress. Only owner facts and
// novel successful receipts count; host advice and repeated claims do not.
func (r *Runner) bookkeepingProgress(ctx context.Context, productive bool) bool {
	fresh := false
	if note := r.enforcement.current; note != nil && productive && note.Error == "" && !note.Result.Failed && !note.Result.Blocked && !note.Result.NoMatches && note.Result.Text != "" {
		basis := note.Tool + "\x00" + note.Result.Text
		if note.Tool == "ask" {
			raw, _ := json.Marshal(note.Args)
			basis += "\x00" + string(raw)
		}
		hash := sha256.Sum256([]byte(basis))
		if r.enforcement.discovered == nil {
			r.enforcement.discovered = map[[32]byte]bool{}
		}
		fresh = !r.enforcement.discovered[hash]
		r.enforcement.discovered[hash] = true // Remember observations even before repair is requested.
	}
	if note := r.enforcement.current; note != nil && note.Result.NoMatches {
		return false
	}
	if r.Assigned == nil {
		return productive
	}
	facts, err := r.Assigned.Facts(ctx)
	if err != nil {
		return false
	}
	if owning, ok := facts.(BookkeepingFacts); ok {
		owning.Preparation = "" // Advice/checkpoint metadata is not a changed owning fact.
		owning.Checkpoint = ProgressCheckpoint{}
		facts = owning
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(raw)
	changed := hash != r.enforcement.facts
	r.enforcement.facts = hash
	return productive && (changed || fresh)
}
