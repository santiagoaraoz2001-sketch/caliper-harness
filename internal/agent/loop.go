package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Supervision is advisory. The owner's fixed confidence floor is independent
// of the configurable authorization and factual-admission threshold.
const (
	advisoryConfidence = .5
	loopStuck          = "the agent should reconsider the repeated thought and take a grounded next step"
	loopProgress       = "the agent is making progress and should continue"
)

// watcher supplies advisory cadence plus cumulative, bounded observed evidence.
// Neither repeated inspection nor complete reads establish task completion.
type watcher struct {
	history       discoveryHistory
	historyBudget int
	lastSig       string
	sameRun       int
	nonprod       int
	// Advisory cadence counts settled receipts independently of their success.
	// Productive discovery and varied Bash calls still need progress supervision.
	discoveryCalls int
	// consecutiveFailures is a judgment trigger: repeated straight failures
	// ask for direction long before the cadence backstop would.
	consecutiveFailures                                  int
	recentReplies                                        []llm.Message
	recentReceipts                                       []*loopObservation
	recentDeliberation                                   []deliberationOmission
	omittedReplies, omittedReceipts, omittedDeliberation int
	source                                               *observed
	advice                                               string
	phaseFacts                                           [32]byte
}

// observe records one executed call or refused reply attempt, returning the trip, or ""
// when quiet. Repeat counts executed calls with identical signatures;
// stall counts attempts with nothing productive.
func (w *watcher) observe(sig string, productive bool, repeatN, stallM int) string {
	if sig != "" && sig == w.lastSig {
		w.sameRun++
	} else if sig != "" {
		w.lastSig, w.sameRun = sig, 1
	} else {
		w.lastSig, w.sameRun = "", 0
	}
	if productive {
		w.nonprod = 0
	} else {
		w.nonprod++
	}
	if repeatN > 0 && w.sameRun >= repeatN {
		return fmt.Sprintf("cadence review: tool %s has %d consecutive receipts with identical arguments; assess their actual relevance", strings.SplitN(sig, " ", 2)[0], w.sameRun)
	}
	if stallM > 0 && w.nonprod >= stallM {
		return fmt.Sprintf("cadence review after %d consecutive attempts; assess new information, recoverable errors and the next legal step", w.nonprod)
	}
	return ""
}

// reset clears the counters after a trip is cleared as progress.
func (w *watcher) reset() {
	w.lastSig, w.sameRun, w.nonprod = "", 0, 0
}

// callSignature canonicalizes a call for repeat comparison. Full values,
// never truncated — truncation could conflate distinct calls.
func callSignature(name string, args tool.Args) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, args[k])
	}
	return b.String()
}

type loopObservation struct {
	CallID                              string     `json:",omitempty"`
	ObservedAt                          *time.Time `json:",omitempty"`
	OutputText                          string     `json:",omitempty"`
	Tool                                string
	Args                                tool.Args
	StateChanged                        bool `json:",omitempty"`
	Changed, Failed, Blocked, Truncated bool
	NoMatches                           bool
	ExitCode                            *int
	Error                               string
	OutputBytes                         int
	OutputSHA256                        string
	ArgumentsSHA256                     string
	TextBasis                           string
}

func loopObserved(note *observed) *loopObservation {
	if note == nil {
		return nil
	}
	basis := "recorded tool result text"
	if note.Tool == "graphql" {
		basis = "GraphQL observation summary; response body omitted"
	}
	var observedAt *time.Time
	args, _ := json.Marshal(note.Args)
	if !note.ObservedAt.IsZero() {
		value := note.ObservedAt
		observedAt = &value
	}
	return &loopObservation{Tool: note.Tool, Args: note.Args, CallID: note.CallID, ObservedAt: observedAt,
		Changed: note.Result.Changed, StateChanged: note.Result.StateChanged, Failed: note.Result.Failed, Blocked: note.Result.Blocked,
		Truncated: note.Result.Truncated, NoMatches: note.Result.NoMatches, ExitCode: note.Result.ExitCode,
		Error: note.Error, OutputBytes: len(note.Result.Text), OutputSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(note.Result.Text))), ArgumentsSHA256: fmt.Sprintf("%x", sha256.Sum256(args)), TextBasis: basis}
}

// Both progress touchpoints use the same observed packet and advisory choices.
// Cadence asks for direction; it cannot end a run or grant execution authority.
func (r *Runner) confirmLoop(ctx context.Context, w *watcher, desc string, log func(string, string) error, refusal string) (string, error) {
	packet, err := r.progressEvidence(ctx, w)
	if err != nil {
		return "", err
	}
	packet.Trip = desc
	if refusal != "" && packet.PreflightRefusal == "" {
		packet.PreflightRefusal = refusal
		packet.Checkpoint.NextAction = r.Prompts.Text("progress.refused_call")
	}
	return r.progressAdvice(ctx, w, packet, decision.SourceLoopConfirm, r.Prompts.Text("decision.loop.instructions"), log)
}

type deliberationOmission struct {
	Field, SHA256  string
	Bytes          int
	OmittedBytes   int
	Provenance     string `json:",omitempty"`
	VerbatimSuffix string `json:",omitempty"`
}

// Dense reasoning stays intact in the raw session and worker continuation.
// The judge receives one labelled suffix using the existing sample budget;
// alternate reasoning fields retain exact omission provenance.
func completedProgressReply(reply llm.Message, sampleBytes int) (llm.Message, []deliberationOmission) {
	copy := reply.Clone()
	var omitted []deliberationOmission
	var latest string
	for _, field := range []struct {
		name string
		text *string
	}{{"thinking", &copy.Thinking}, {"reasoning", &copy.Reasoning}, {"reasoning_content", &copy.ReasoningContent}} {
		if *field.text == "" {
			continue
		}
		latest = *field.text
		omitted = append(omitted, deliberationOmission{Field: field.name, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(latest))), Bytes: len(latest), OmittedBytes: len(latest)})
		*field.text = ""
	}
	if len(omitted) > 0 && sampleBytes > 0 {
		e := &omitted[len(omitted)-1]
		e.Provenance = "unverified completed model deliberation; verbatim suffix, not observed tool evidence"
		e.VerbatimSuffix = deliberationSuffix(latest, sampleBytes)
		e.OmittedBytes -= len(e.VerbatimSuffix)
	}
	return copy, omitted
}

type progressOmission struct {
	Field, SHA256 string
	Bytes         int
}

type unexecutedProgressCall struct{ CallID, Tool, Disposition string }

type deliveredUserComment struct {
	EntryIndex int
	Text       string
}

// Only delivered host-framed notes are steering. User-role tool results and
// harness guidance are not human requests; task advice keeps its append source.
func (r *Runner) deliveredUserContext() (original, current string, comments []deliveredUserComment, taskComments []outcome.TaskComment) {
	for i, e := range r.Continuation.Entries {
		if e.Message.Role != "user" {
			continue
		}
		switch e.Kind {
		case "intent":
			original = e.Message.Content
		case "task":
			current = e.Message.Content
		case "note":
			if strings.HasPrefix(e.Message.Content, "USER COMMENT: ") {
				comments = append(comments, deliveredUserComment{i, e.Message.Content})
			} else if raw, ok := strings.CutPrefix(e.Message.Content, r.Prompts.Text("continuation.task_comment_prefix")); ok {
				var comment outcome.TaskComment
				if json.Unmarshal([]byte(raw), &comment) == nil && comment.ID != "" {
					taskComments = append(taskComments, comment)
				}
			}
		}
	}
	if current == original {
		current = ""
	}
	return
}

// ReadinessRequirements reuses delivered-user provenance. User-role tool
// results, harness advice and notes still queued for delivery are not requests.
func (r *Runner) ReadinessRequirements() string {
	original, current, comments, _ := r.deliveredUserContext()
	if original == "" && current == "" && len(comments) == 0 {
		return ""
	}
	raw, _ := json.Marshal(struct {
		OriginalUserIntent, CurrentUserRequest string
		DeliveredUserComments                  []deliveredUserComment `json:",omitempty"`
	}{original, current, comments})
	return string(raw)
}

type progressPacket struct {
	// Only the owning reviewer runner may identify its assembled review request
	// as attachments, rather than human intent. Never inferred from its text.
	hostReviewRequest                                                    bool
	AnsweredQuestions                                                    []session.Entry `json:",omitempty"`
	LatestSettled                                                        *loopObservation
	UnexecutedCalls                                                      []unexecutedProgressCall `json:",omitempty"`
	CapacityOmissions                                                    []progressOmission       `json:",omitempty"`
	OriginalUserIntent, CurrentUserRequest                               string
	AuthoredTaskInstructions                                             string                 `json:",omitempty"` // frozen requirements, never observed evidence or permission
	DeliveredUserComments                                                []deliveredUserComment `json:",omitempty"`
	DeliveredTaskComments                                                []outcome.TaskComment  `json:",omitempty"`
	PlanningActive, CurrentClaim                                         bool
	RecentCompletedReplies                                               []llm.Message
	RecentReceipts                                                       []*loopObservation
	OmittedDeliberation                                                  []deliberationOmission
	OmittedCompletedReplies, OmittedReceipts, OmittedDeliberationEntries int
	LatestSourceText                                                     string
	Discovery                                                            discoveryEvidence
	Coding                                                               *session.CodingState
	Facts                                                                any
	Checkpoint                                                           ProgressCheckpoint
	RemainingSteps, RemainingSeconds                                     *int
	PreviousAdvice, PendingRepair                                        string
	Trip, PreflightRefusal, ProgressBasis                                string `json:",omitempty"`
	CurrentToolSchema                                                    string `json:",omitempty"`
	Latest, LatestDiscovery                                              *loopObservation
	Assigned, Settled                                                    bool
	HistoricalSettlement                                                 *TaskSettlement   `json:",omitempty"`
	UnfinishedResponse                                                   *unfinishedSample `json:",omitempty"`
	// Historical review attachments follow current role, evidence and action.
	Review               *ReviewInputs     `json:",omitempty"`
	HostReviewAttachment *progressOmission `json:",omitempty"`
}

func (r *Runner) progressEvidence(ctx context.Context, w *watcher) (progressPacket, error) {
	var facts any
	var err error
	if r.Assigned != nil && r.PlanningActive() {
		facts, err = r.Assigned.Facts(ctx)
		if err != nil {
			return progressPacket{}, err
		}
	}
	packet := progressPacket{PlanningActive: r.PlanningActive(),
		ProgressBasis:            r.Prompts.Text("decision.loop.progress_basis"),
		AuthoredTaskInstructions: r.TaskSystem,
		Coding:                   r.Continuation.Coding, Facts: facts, Checkpoint: r.currentProgress(facts), PendingRepair: r.enforcement.repair,
		Latest: loopObserved(r.enforcement.current), LatestDiscovery: loopObserved(r.enforcement.discovery), Assigned: r.assignedClaim, Settled: r.settlement != nil}
	if a := r.Continuation.Assignment; a != nil {
		packet.CurrentClaim = r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})
	}
	if r.settlement != nil {
		copy := *r.settlement
		packet.HistoricalSettlement = &copy
	}
	if !r.PlanningActive() {
		packet.Checkpoint.Phase, packet.Checkpoint.PATStep, packet.Checkpoint.NextAction = "EXECUTE", "not-selected", r.Prompts.Text("progress.ordinary")
		if c := r.Continuation.Coding; c != nil {
			packet.Checkpoint.SelectedID = c.TaskID
			if c.Passed() {
				packet.Checkpoint.Phase = "VALIDATE"
			}
		}
	}
	if r.Reviewing {
		packet.Checkpoint.Phase, packet.Checkpoint.PATStep = "REVIEW", "independent-review"
		packet.Checkpoint.NextAction = r.Prompts.Text("progress.review")
	}
	packet.OriginalUserIntent, packet.CurrentUserRequest, packet.DeliveredUserComments, packet.DeliveredTaskComments = r.deliveredUserContext()
	packet.hostReviewRequest = r.reviewRequest != "" && packet.OriginalUserIntent == r.reviewRequest
	for i, keep := range answerGroupEntries(r.Continuation) {
		if keep {
			packet.AnsweredQuestions = append(packet.AnsweredQuestions, r.Continuation.Entries[i])
		}
	}
	if w != nil {
		packet.Discovery = w.history.evidence()
		for _, message := range w.recentReplies {
			packet.RecentCompletedReplies = append(packet.RecentCompletedReplies, message.Clone())
		}
		for _, receipt := range w.recentReceipts {
			copy := *receipt
			packet.RecentReceipts = append(packet.RecentReceipts, &copy)
		}
		if len(packet.RecentReceipts) > 0 {
			copy := *packet.RecentReceipts[len(packet.RecentReceipts)-1]
			copy.OutputText = ""
			packet.LatestSettled = &copy
		}
		windowCalls := map[string]bool{}
		for _, message := range w.recentReplies {
			for _, call := range message.ToolCalls {
				windowCalls[call.ID] = true
			}
		}
		var latestNativeResult *llm.Message
		for _, entry := range r.Continuation.Entries {
			message := entry.Message
			if message.Role == "tool" && windowCalls[message.ToolCallID] {
				latestNativeResult = &message
				if strings.HasPrefix(message.Content, "call not executed:") || strings.Contains(message.Content, "entire call array refused; no calls ran") {
					packet.UnexecutedCalls = append(packet.UnexecutedCalls, unexecutedProgressCall{message.ToolCallID, message.Name, message.Content})
				}
			}
		}
		// A newer successful result replaces an older rejected-call focus. A
		// skipped array tail is not the failed operation and gains no schema.
		if latestNativeResult != nil && strings.Contains(latestNativeResult.Content, "entire call array refused; no calls ran") {
			names := []string{latestNativeResult.Name}
			for _, reply := range w.recentReplies {
				for _, call := range reply.ToolCalls {
					if call.ID == latestNativeResult.ToolCallID {
						names = nil
						for _, submitted := range reply.ToolCalls {
							names = append(names, submitted.Name)
						}
						break
					}
				}
			}
			r.groundProgressRefusal(&packet, latestNativeResult.Content, names...)
		} else if len(packet.RecentReceipts) > 0 {
			last := packet.RecentReceipts[len(packet.RecentReceipts)-1]
			current := latestNativeResult == nil || latestNativeResult.ToolCallID == last.CallID || strings.HasPrefix(latestNativeResult.Content, "call not executed:")
			// Executed nonzero results are evidence, not a malformed-call refusal.
			if current && (last.Blocked || (last.ExitCode == nil && (last.Error != "" || last.Failed))) {
				refusal := last.Error
				if last.OutputText != "" {
					if refusal != "" {
						refusal += "\n"
					}
					refusal += last.OutputText
				}
				r.groundProgressRefusal(&packet, refusal, last.Tool)
			}
		}
		// Text compatibility retains its one-block protocol. Its latest actual
		// failure is paired with the completed parsed call, never guessed from
		// error prose or an unfinished response.
		if latestNativeResult == nil && len(w.recentReplies) > 0 && len(r.Continuation.Entries) > 0 {
			last := r.Continuation.Entries[len(r.Continuation.Entries)-1]
			if last.Kind == "failure" && last.Message.Role == "user" && (r.enforcement.current == nil || r.enforcement.current.Result.ExitCode == nil) {
				call, found, _ := tool.Extract(w.recentReplies[len(w.recentReplies)-1].Content, r.Prompts)
				if found && call.Name != "" {
					r.groundProgressRefusal(&packet, last.Message.Content, call.Name)
				}
			}
		}
		packet.OmittedDeliberation, packet.PreviousAdvice = append([]deliberationOmission(nil), w.recentDeliberation...), w.advice
		packet.OmittedCompletedReplies, packet.OmittedReceipts, packet.OmittedDeliberationEntries = w.omittedReplies, w.omittedReceipts, w.omittedDeliberation
		if w.source != nil {
			packet.LatestSourceText = w.source.Result.Text
		}
	}
	packet.RemainingSteps = r.remainingSteps(packet.Checkpoint.RunStep)
	packet.RemainingSeconds = r.remainingRunSeconds(packet.Checkpoint.ElapsedSeconds)
	packet.projectReview(r.ReviewInputs)
	return packet, nil
}

// Reuse the current request registry and recorded refusal. This is an advisory
// projection, not another claim, ledger or execution path.
func (r *Runner) groundProgressRefusal(packet *progressPacket, refusal string, names ...string) {
	packet.PreflightRefusal = refusal
	packet.Checkpoint.NextAction = r.Prompts.Text("progress.refused_call")
	if len(names) > 1 {
		packet.Checkpoint.NextAction = r.Prompts.Text("progress.refused_array")
	}
	if r.Registry == nil {
		return
	}
	seen := map[string]bool{}
	var current []string
	for _, name := range names {
		if def, available := r.Registry.ResolveName(name); available {
			if !seen[def.Name] {
				current = append(current, def.Schema())
				seen[def.Name] = true
			}
		} else if !seen[name] {
			current = append(current, r.Prompts.Format("progress.call_unavailable", name, strings.Join(r.Registry.Names(), ", ")))
			seen[name] = true
		}
	}
	packet.CurrentToolSchema = strings.Join(current, "\n")
}

func (r *Runner) progressAdvice(ctx context.Context, w *watcher, packet progressPacket, source, instructions string, log func(string, string) error) (string, error) {
	reviewedPhase := r.progressPhase(packet)
	packet.deduplicateEvidence()
	options := []string{r.Prompts.Text("decision.discovery.continue"), r.Prompts.Text("decision.discovery.act"), r.Prompts.Text("decision.discovery.reconsider")}
	req, v, err := r.progressJudgment(ctx, &packet, decision.Request{Instructions: instructions, Options: options}, packet.reduceForCapacity, log)
	failed := func(cause error) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := log("judgment-error", fmt.Sprintf("source=%s error=%q", source, cause)); err != nil {
			return "", err
		}
		if w != nil {
			w.phaseFacts = reviewedPhase
			if r.LoopStall > 0 {
				w.discoveryCalls %= r.LoopStall
			}
		}
		return "", nil // Failed direction advice adds nothing to the executor's context.
	}
	if err != nil {
		var unavailable *progressJudgeError
		if errors.As(err, &unavailable) {
			return failed(err)
		}
		return "", err // Encoding and evidence writes remain authoritative failures.
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	index := -1
	for i, option := range options {
		if v.Option == option {
			index = i
		}
	}
	if index < 0 {
		return failed(fmt.Errorf("progress advice returned unsupported option %q", v.Option))
	}
	keys := []string{"", "continuation.discovery.act", "continuation.discovery.reconsider"}
	advice := ""
	resolution := []string{"continued-work", "repair-rejected-call", "reconsider-approach"}[index]
	if v.Confidence < advisoryConfidence {
		resolution = "failed-confidence"
	} else if index == 1 && packet.PreflightRefusal == "" {
		resolution = "unsupported-repair" // No unresolved refusal: stale advice cannot become a new instruction.
	} else if index != 0 {
		advice = r.Prompts.Text(keys[index])
	}
	if advice != "" {
		if packet.PreflightRefusal != "" && packet.CurrentToolSchema != "" {
			advice += "\n" + r.Prompts.Format("continuation.progress.current_call", packet.PreflightRefusal, packet.CurrentToolSchema)
		} else if (r.PlanningActive() || r.Reviewing) && packet.Checkpoint.NextAction != "" {
			advice += r.Prompts.Text("continuation.progress.next_action_prefix") + packet.Checkpoint.NextAction
		}
	}
	var saved error
	decision.LogJudgment(func(kind, text string) {
		if saved == nil {
			saved = log(kind, text)
		}
	}, source, req, v, resolution, r.JudgmentContextChars)
	if saved != nil {
		return "", saved
	}
	if advice != "" {
		if err := log("guidance", advice); err != nil {
			return "", err
		}
	}
	if w != nil {
		// Both touchpoints reviewed this settled phase, including silent verdicts.
		w.phaseFacts = reviewedPhase
		if r.LoopStall > 0 {
			w.discoveryCalls %= r.LoopStall
		}
		if advice != "" {
			w.advice = advice
		}
	}
	return advice, nil
}

// Keep one exact argument body per call in the judge projection. The same
// write/edit often appears in Latest, LatestSettled, the receipt window and the
// assistant reply. References retain call identity and a digest; original
// observations, worker context and failure receipts are never rewritten.
func (p *progressPacket) deduplicateEvidence() {
	// The existing sample budget is shared by the packet, not each old reply.
	kept := p.UnfinishedResponse != nil && p.UnfinishedResponse.Text != ""
	for i := len(p.OmittedDeliberation) - 1; i >= 0; i-- {
		e := &p.OmittedDeliberation[i]
		if e.VerbatimSuffix == "" {
			continue
		}
		if !kept {
			kept = true
			continue
		}
		e.VerbatimSuffix, e.OmittedBytes = "", e.Bytes
		e.Provenance = "older unverified completed deliberation; full original bytes and digest retained"
	}
	seen := map[string]string{}
	dedup := func(field, id string, raw []byte, remove, restore func()) {
		if id == "" || len(raw) == 0 || string(raw) == "null" || !json.Valid(raw) {
			return
		}
		if _, err := tool.JSONObject(string(raw)); err != nil {
			return // Ambiguous/malformed arguments remain literal evidence.
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return
		}
		key := id + ":" + string(canonical)
		if previous, ok := seen[key]; ok {
			p.omitSmaller(field+" (same call arguments as "+previous+")", string(raw), remove, restore)
		} else {
			seen[key] = field
		}
	}
	for _, item := range []struct {
		name    string
		receipt *loopObservation
	}{{"Latest", p.Latest}, {"LatestDiscovery", p.LatestDiscovery}, {"LatestSettled", p.LatestSettled}} {
		if item.receipt == nil {
			continue
		}
		receipt := item.receipt
		args := receipt.Args
		raw, _ := json.Marshal(args)
		dedup(item.name+".Args", receipt.CallID, raw, func() { receipt.Args = nil }, func() { receipt.Args = args })
	}
	for i, receipt := range p.RecentReceipts {
		args := receipt.Args
		raw, _ := json.Marshal(args)
		dedup(fmt.Sprintf("RecentReceipts[%d].Args", i), receipt.CallID, raw, func() { receipt.Args = nil }, func() { receipt.Args = args })
	}
	for i := range p.RecentCompletedReplies {
		for j := range p.RecentCompletedReplies[i].ToolCalls {
			call := &p.RecentCompletedReplies[i].ToolCalls[j]
			args := call.Arguments
			dedup(fmt.Sprintf("RecentCompletedReplies[%d].ToolCalls[%d].Arguments", i, j), call.ID, []byte(args), func() { call.Arguments = "" }, func() { call.Arguments = args })
		}
	}
}

func (r *Runner) progressPhase(packet progressPacket) [32]byte {
	return owningFactsDigest(struct {
		Workflow                   *session.WorkflowScope
		Phase, PATStep, SelectedID string
		Claimed                    bool
	}{r.Continuation.Workflow, packet.Checkpoint.Phase, packet.Checkpoint.PATStep, packet.Checkpoint.SelectedID, r.assignedClaim})
}

// Advisory callers (discovery advice and PAT/product enforcement) may
// continue after a failed Judge call. Encoding, recording and all other
// callers retain their existing error semantics.
type progressJudgeError struct{ error }

func (e *progressJudgeError) Unwrap() error { return e.error }

// The opt-in caller owns evidence selection. Other judgments never reduce or
// retry; every native admission request and final selected input is recorded.
func (r *Runner) progressJudgment(ctx context.Context, input any, req decision.Request, reduce func() bool, log func(string, string) error) (decision.Request, decision.Verdict, error) {
	for admission := 0; ; admission++ {
		raw, err := json.Marshal(input)
		if err != nil {
			return req, decision.Verdict{}, err
		}
		req.Context = string(raw)
		// The stored copy is capped like stored verdict contexts: the full
		// packet (up to the decision input token ceiling) would otherwise
		// be duplicated into the session store on every judgment. The
		// judge still receives the complete request; raw evidence stays
		// in the session's own reply history.
		stored := req.Context
		if r.JudgmentContextChars > 0 {
			if runes := []rune(stored); len(runes) > r.JudgmentContextChars {
				stored = string(runes[:r.JudgmentContextChars]) + fmt.Sprintf("…[%d bytes total, sha256=%x; stored copy truncated]", len(raw), sha256.Sum256(raw))
			}
		}
		if err := log("judgment-input", stored); err != nil {
			return req, decision.Verdict{}, err
		}
		v, err := r.Judge(ctx, req)
		if err == nil {
			return req, v, nil
		}
		var capacity *decision.ContextCapacityError
		if !errors.As(err, &capacity) || admission >= 12 {
			return req, decision.Verdict{}, &progressJudgeError{fmt.Errorf("progress advice unavailable: %w", err)}
		}
		// A refusal is local admission, not inference. Batch optional omissions
		// before retrying: one refusal per small field caused thousands of native
		// admissions and duplicated log packets on long tasks. Protected evidence
		// remains owned by reduce; never truncate required facts to force a fit.
		target := len(raw) / 2
		removed := 0
		for reduce() {
			removed++
			if e := ctx.Err(); e != nil {
				return req, decision.Verdict{}, e
			}
			next, e := json.Marshal(input)
			if e != nil {
				return req, decision.Verdict{}, e
			}
			if len(next) <= target {
				break
			}
		}
		if removed == 0 {
			return req, decision.Verdict{}, &progressJudgeError{fmt.Errorf("progress advice unavailable: protected evidence exceeds capacity: %w", err)}
		}
		if err := log("note", fmt.Sprintf("progress input refused before inference; retrying after %d disclosed optional evidence omissions", removed)); err != nil {
			return req, decision.Verdict{}, err
		}
	}
}

// Only a real native capacity refusal invokes this reduction. Original/latest
// user intent, answered questions, latest failure and host phase remain intact.
// Every removed body is still in the raw session and retains an exact digest.
// Keep concise useful evidence when omission metadata would make it larger.
// This compares actual encoded bytes; it is not a new production limit.
func (p *progressPacket) omitSmaller(field, text string, remove, restore func()) bool {
	before, err := json.Marshal(p)
	if err != nil {
		return false
	}
	original := len(p.CapacityOmissions)
	p.CapacityOmissions = append(p.CapacityOmissions, progressOmission{field, fmt.Sprintf("%x", sha256.Sum256([]byte(text))), len(text)})
	remove()
	after, err := json.Marshal(p)
	if err == nil && len(after) < len(before) {
		return true
	}
	restore()
	p.CapacityOmissions = p.CapacityOmissions[:original]
	return false
}

func (p *progressPacket) reduceForCapacity() bool {
	// Known review attachments were decomposed at normal projection. Unknown
	// or restored prompts may contain unique obligations; capacity alone does
	// not make their contents disposable.
	// Historical reviewer prose is optional; the current rejected task's
	// failure feedback remains protected along with its owning outcome.
	if s := p.HistoricalSettlement; s != nil && (s.Qualified || s.TaskID != p.Checkpoint.SelectedID) {
		feedback := s.Feedback
		if feedback != "" && p.omitSmaller("HistoricalSettlement.Feedback", feedback, func() { s.Feedback = "" }, func() { s.Feedback = feedback }) {
			return true
		}
	}
	for i := range p.OmittedDeliberation {
		e := &p.OmittedDeliberation[i]
		previous := *e
		if e.VerbatimSuffix != "" && p.omitSmaller(fmt.Sprintf("OmittedDeliberation[%d].VerbatimSuffix (unverified model deliberation)", i), e.VerbatimSuffix, func() { e.VerbatimSuffix = ""; e.OmittedBytes = e.Bytes }, func() { *e = previous }) {
			return true
		}
	}
	for i := range p.RecentCompletedReplies {
		message := &p.RecentCompletedReplies[i]
		for j := range message.ToolCalls {
			call := &message.ToolCalls[j]
			text := call.Arguments
			if text != "" && call.Name != "ask" && p.omitSmaller(fmt.Sprintf("RecentCompletedReplies[%d].ToolCalls[%d].Arguments (call %s)", i, j, call.ID), text, func() { call.Arguments = "" }, func() { call.Arguments = text }) {
				return true
			}
		}
		text := message.Content
		if text != "" && p.omitSmaller(fmt.Sprintf("RecentCompletedReplies[%d].Content (unverified model claim)", i), text, func() { message.Content = "" }, func() { message.Content = text }) {
			return true
		}
	}
	for i, receipt := range p.RecentReceipts {
		latestFailure := p.LatestSettled != nil && p.LatestSettled.CallID == receipt.CallID && (p.LatestSettled.Error != "" || p.LatestSettled.Failed || p.LatestSettled.Blocked || p.LatestSettled.ExitCode != nil && *p.LatestSettled.ExitCode != 0)
		if receipt.Tool == "ask" || latestFailure {
			continue
		}
		text := receipt.OutputText
		if text != "" && p.omitSmaller(fmt.Sprintf("RecentReceipts[%d].OutputText (call %s)", i, receipt.CallID), text, func() { receipt.OutputText = "" }, func() { receipt.OutputText = text }) {
			return true
		}
		args := receipt.Args
		raw, err := json.Marshal(args)
		if len(args) > 0 && err == nil && p.omitSmaller(fmt.Sprintf("RecentReceipts[%d].Args (call %s)", i, receipt.CallID), string(raw), func() { receipt.Args = nil }, func() { receipt.Args = args }) {
			return true
		}
	}
	for _, receipt := range []*loopObservation{p.Latest, p.LatestDiscovery, p.LatestSettled} {
		if receipt == nil || receipt.Tool == "ask" || receipt.Error != "" || receipt.Failed || receipt.Blocked || receipt.ExitCode != nil && *receipt.ExitCode != 0 || len(receipt.Args) == 0 {
			continue
		}
		args := receipt.Args
		raw, err := json.Marshal(args)
		if err == nil && p.omitSmaller("latest receipt optional arguments (call "+receipt.CallID+")", string(raw), func() { receipt.Args = nil }, func() { receipt.Args = args }) {
			return true
		}
	}
	source := p.LatestSourceText
	if source != "" && p.omitSmaller("LatestSourceText", source, func() { p.LatestSourceText = "" }, func() { p.LatestSourceText = source }) {
		return true
	}
	// Cumulative provenance outlives recent receipt bodies, but remains optional
	// under an actual capacity refusal. Disclose lost facts rather than treating
	// absent historical evidence as novel work or proof of no earlier inspection.
	if len(p.Discovery.Sources)+len(p.Discovery.RepeatedObservations) > 0 {
		previous := p.Discovery
		raw, _ := json.Marshal(previous)
		if p.omitSmaller("Discovery", string(raw), func() {
			p.Discovery = discoveryEvidence{OmittedFacts: previous.OmittedFacts + len(previous.Sources) + len(previous.RepeatedObservations)}
		}, func() { p.Discovery = previous }) {
			return true
		}
	}
	// Checkpoint retains actual host phase/identity/next action if a large
	// authored snapshot is omitted; no qualification or authority is created.
	if facts, ok := p.Facts.(BookkeepingFacts); ok {
		previous := p.Facts
		if len(facts.Unfinished) > 0 && p.omitSmaller("Facts.Unfinished", string(facts.Unfinished), func() { copy := facts; copy.Unfinished = nil; p.Facts = copy }, func() { p.Facts = previous }) {
			return true
		}
		raw, err := json.Marshal(facts.Knowledge)
		if len(facts.Knowledge) > 0 && err == nil && p.omitSmaller("Facts.Knowledge", string(raw), func() { copy := facts; copy.Knowledge = nil; p.Facts = copy }, func() { p.Facts = previous }) {
			return true
		}
	}
	return false
}

// completedDiscovery retains the cadence-sized window of complete replies before
// maintenance can retire duplicates. Disabling optional cadence still retains one
// latest completed boundary for current refusal/ownership decisions.
func (w *watcher) completedDiscovery(reply llm.Message, cadence int, sampleBytes ...int) {
	if cadence <= 0 {
		cadence = 1
	}
	budget := 0 // Legacy callers without configured sampling retain omission-only evidence.
	if len(sampleBytes) > 0 {
		budget = sampleBytes[0]
	}
	copy, omitted := completedProgressReply(reply, budget)
	w.recentReplies = append(w.recentReplies, copy)
	w.recentDeliberation = append(w.recentDeliberation, omitted...)
	if len(w.recentDeliberation) > cadence {
		w.omittedDeliberation += len(w.recentDeliberation) - cadence
		w.recentDeliberation = w.recentDeliberation[len(w.recentDeliberation)-cadence:]
	}
	if len(w.recentReplies) > cadence {
		w.omittedReplies += len(w.recentReplies) - cadence
		w.recentReplies = w.recentReplies[len(w.recentReplies)-cadence:]
	}
}
func (w *watcher) discoveryReceipt(note *observed, cadence int) {
	if note == nil {
		return
	}
	if cadence <= 0 {
		cadence = 1
	}
	w.history.record(note, w.historyBudget)
	receipt := loopObserved(note)
	switch note.Tool {
	case "read", "read_outside", "grep":
		if note.Error == "" && !note.Result.Failed && !note.Result.Blocked && (note.Result.ExitCode == nil || *note.Result.ExitCode == 0) {
			receipt.TextBasis += "; only the latest successful source body is supplied in LatestSourceText"
		} else {
			receipt.OutputText = note.Result.Text
		}
	default:
		receipt.OutputText = note.Result.Text
	}
	w.recentReceipts = append(w.recentReceipts, receipt)
	w.discoveryCalls++ // An advisory touchpoint, never a diagnosis or an execution ceiling.
	if note.Error != "" || note.Result.Failed || note.Result.Blocked {
		w.consecutiveFailures++
	} else {
		w.consecutiveFailures = 0
	}
	if len(w.recentReceipts) > cadence {
		w.omittedReceipts += len(w.recentReceipts) - cadence
		w.recentReceipts = w.recentReceipts[len(w.recentReceipts)-cadence:]
	}
	switch note.Tool {
	case "read", "read_outside", "find", "find_outside", "find_file", "find_dir", "grep":
		if note.Error == "" && !note.Result.Failed && !note.Result.Blocked {
			if !note.Result.NoMatches && (note.Tool == "read" || note.Tool == "read_outside" || note.Tool == "grep") {
				w.source = note
			}
		}
	}
}

// reviewDiscovery runs only after all correlated results of a completed reply settle.
// Its cadence and real lifecycle changes ask about direction, not whether work stalled.
func (r *Runner) reviewDiscovery(ctx context.Context, w *watcher, log func(string, string) error, unfinished ...llm.Message) (string, error) {
	limitBoundary := len(unfinished) > 0
	if w == nil || !limitBoundary && r.LoopStall <= 0 || r.Session == nil || r.SessionID == "" {
		return "", nil
	}
	packet, err := r.progressEvidence(ctx, w)
	if err != nil {
		return "", err
	}
	digest := r.progressPhase(packet)
	phaseChanged := w.phaseFacts != ([32]byte{}) && digest != w.phaseFacts
	// A successful host-owned drafting transition already supplies its next
	// exact question. It is ordinary progress, not a new lifecycle concern.
	// Keep cadence, current refusals and real claim/review transitions intact.
	if note := r.enforcement.latest; !packet.CurrentClaim && note != nil && note.PlanningQuestion != "" && note.PlanningQuestion == packet.Checkpoint.NextAction && note.Error == "" && !note.Result.Failed && !note.Result.Blocked && !note.Result.Truncated && packet.PreflightRefusal == "" {
		phaseChanged = false
		w.phaseFacts = digest
	}
	if w.phaseFacts == ([32]byte{}) {
		w.phaseFacts = digest
	}
	stuck := w.consecutiveFailures >= 3
	if !limitBoundary && !phaseChanged && !stuck && w.discoveryCalls < r.LoopStall {
		return "", nil
	}
	w.consecutiveFailures = 0 // One review per failure run, not one per failure.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if limitBoundary {
		packet.UnfinishedResponse, err = r.sampleUnfinished(unfinished[0])
		if err != nil {
			return "", err
		}
	}
	return r.progressAdvice(ctx, w, packet, "discovery-checkpoint", r.Prompts.Text("decision.discovery.instructions"), log)
}

type unfinishedSample struct {
	Provenance, Field, Text                       string
	OriginalBytes, OmittedBytes, NativeCallDrafts int
}

func (r *Runner) sampleUnfinished(message llm.Message) (*unfinishedSample, error) {
	if r.ThinkingLoop.SampleBytes <= 0 {
		return nil, fmt.Errorf("response-limit progress judgment needs configured agent.thinking_loop.sample_bytes")
	}
	field, text := "content", message.Content
	for _, auxiliary := range []struct{ field, text string }{{"thinking", message.Thinking}, {"reasoning", message.Reasoning}, {"reasoning_content", message.ReasoningContent}} {
		if auxiliary.text != "" {
			field, text = auxiliary.field, auxiliary.text
		}
	}
	suffix := deliberationSuffix(text, r.ThinkingLoop.SampleBytes)
	return &unfinishedSample{"unverified partial model deliberation; no calls from this unfinished response executed; literal prefix unchanged", field, suffix, len(text), len(text) - len(suffix), len(message.ToolCalls)}, nil
}

func deliberationSuffix(text string, sampleBytes int) string {
	start := max(0, len(text)-sampleBytes)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
