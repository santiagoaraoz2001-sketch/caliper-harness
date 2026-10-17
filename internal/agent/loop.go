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

// watcher counts consecutive repeat signatures and non-productive attempts.
// Detection is consecutive-attempts by design: it catches the loop-as-stupidity
// shape (identical block N times). A loop interleaved with unrelated work
// evades it — documented, not handled; revisit with live evidence.
type watcher struct {
	lastSig string
	sameRun int
	nonprod int
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
	Changed, Failed, Blocked, Truncated bool
	NoMatches                           bool
	ExitCode                            *int
	Error                               string
	OutputBytes                         int
	OutputSHA256                        string
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
	if !note.ObservedAt.IsZero() {
		value := note.ObservedAt
		observedAt = &value
	}
	return &loopObservation{Tool: note.Tool, Args: note.Args, CallID: note.CallID, ObservedAt: observedAt,
		Changed: note.Result.Changed, Failed: note.Result.Failed, Blocked: note.Result.Blocked,
		Truncated: note.Result.Truncated, NoMatches: note.Result.NoMatches, ExitCode: note.Result.ExitCode,
		Error: note.Error, OutputBytes: len(note.Result.Text), OutputSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(note.Result.Text))), TextBasis: basis}
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
	packet.ProgressBasis = r.Prompts.Text("decision.loop.progress_basis")
	return r.progressAdvice(ctx, w, packet, decision.SourceLoopConfirm, r.Prompts.Text("decision.loop.instructions"), log)
}

type deliberationOmission struct {
	Field, SHA256 string
	Bytes         int
}

// Only complete model messages are claims. Dense reasoning stays in the raw
// session; the judge sees its provenance rather than duplicate internal drafts.
func completedProgressReply(reply llm.Message) (llm.Message, []deliberationOmission) {
	copy := reply.Clone()
	var omitted []deliberationOmission
	for _, field := range []struct {
		name string
		text *string
	}{{"thinking", &copy.Thinking}, {"reasoning", &copy.Reasoning}, {"reasoning_content", &copy.ReasoningContent}} {
		if *field.text == "" {
			continue
		}
		omitted = append(omitted, deliberationOmission{field.name, fmt.Sprintf("%x", sha256.Sum256([]byte(*field.text))), len(*field.text)})
		*field.text = ""
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

type progressPacket struct {
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
	Coding                                                               *session.CodingState
	Facts                                                                any
	Checkpoint                                                           ProgressCheckpoint
	RemainingSteps, RemainingSeconds                                     *int
	PreviousAdvice, PendingRepair                                        string
	Trip, PreflightRefusal, ProgressBasis                                string `json:",omitempty"`
	CurrentToolSchema                                                    string `json:",omitempty"`
	Latest, LatestDiscovery                                              *loopObservation
	Assigned, Settled                                                    bool
	UnfinishedResponse                                                   *unfinishedSample `json:",omitempty"`
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
	packet := progressPacket{PlanningActive: r.PlanningActive(), CurrentClaim: r.assignedClaim,
		AuthoredTaskInstructions: r.TaskSystem,
		Coding:                   r.Continuation.Coding, Facts: facts, Checkpoint: r.currentProgress(facts), PendingRepair: r.enforcement.repair,
		Latest: loopObserved(r.enforcement.current), LatestDiscovery: loopObserved(r.enforcement.discovery), Assigned: r.assignedClaim, Settled: r.settlement != nil}
	if !r.PlanningActive() {
		packet.Checkpoint.Phase, packet.Checkpoint.PATStep, packet.Checkpoint.NextAction = "EXECUTE", "not-selected", r.Prompts.Text("progress.ordinary")
		if c := r.Continuation.Coding; c != nil {
			packet.Checkpoint.SelectedID = c.TaskID
			if c.Passed() {
				packet.Checkpoint.Phase = "VALIDATE"
			}
		}
	}
	packet.OriginalUserIntent, packet.CurrentUserRequest, packet.DeliveredUserComments, packet.DeliveredTaskComments = r.deliveredUserContext()
	if w != nil {
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
			if current && (last.Error != "" || last.Failed || last.Blocked || last.ExitCode != nil && *last.ExitCode != 0) {
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
			if last.Kind == "failure" && last.Message.Role == "user" {
				call, found, _ := tool.Extract(w.recentReplies[len(w.recentReplies)-1].Content, r.Prompts)
				if found && call.Name != "" {
					r.groundProgressRefusal(&packet, last.Message.Content, call.Name)
				}
			}
		}
		packet.OmittedDeliberation, packet.PreviousAdvice = w.recentDeliberation, w.advice
		packet.OmittedCompletedReplies, packet.OmittedReceipts, packet.OmittedDeliberationEntries = w.omittedReplies, w.omittedReceipts, w.omittedDeliberation
		if w.source != nil {
			packet.LatestSourceText = w.source.Result.Text
		}
	}
	packet.RemainingSteps = r.remainingSteps(packet.Checkpoint.RunStep)
	packet.RemainingSeconds = r.remainingRunSeconds(packet.Checkpoint.ElapsedSeconds)
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
	} else if index != 0 {
		advice = r.Prompts.Text(keys[index])
	}
	if advice != "" {
		if packet.PreflightRefusal != "" && packet.CurrentToolSchema != "" {
			advice += "\n" + r.Prompts.Format("continuation.progress.current_call", packet.PreflightRefusal, packet.CurrentToolSchema)
		} else if r.PlanningActive() && packet.Checkpoint.NextAction != "" {
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
	for {
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
		if !errors.As(err, &capacity) || !reduce() {
			return req, decision.Verdict{}, &progressJudgeError{fmt.Errorf("progress advice unavailable: %w", err)}
		}
		if err := log("note", "progress input refused before inference by native context admission; retrying with disclosed optional evidence omissions"); err != nil {
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

// completedDiscovery retains the cadence-sized window of complete replies before maintenance
// can retire duplicates. It never consumes partial streamed reasoning.
func (w *watcher) completedDiscovery(reply llm.Message, cadence int) {
	if cadence <= 0 {
		return
	}
	copy, omitted := completedProgressReply(reply)
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
	if note == nil || cadence <= 0 {
		return
	}
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
	start := max(0, len(text)-r.ThinkingLoop.SampleBytes)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return &unfinishedSample{"unverified partial model deliberation; no calls from this unfinished response executed; literal prefix unchanged", field, text[start:], len(text), start, len(message.ToolCalls)}, nil
}
