package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/prompts"
	"strings"
	"unicode"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Compaction carries the context.yaml percents: Trigger fires compaction
// at this emergency percent of the window. Routine maintenance is deterministic.
// Keep fits the latest messages into this percent verbatim; Summary is a concise
// writing target, growing by Step up to Max. Complete summaries are admitted by
// the full retained continuation estimate, not an editorial word quota.
// Trigger 0 disables automatic summaries.
type Compaction struct {
	Trigger       float64
	Keep          float64
	Summary       float64
	Step          float64
	Max           float64
	WordsPerToken float64
}

var compactionRecordFailure = errors.New("compaction recording failed")

// errCompactionDeferred marks a summary that would not reduce retained
// context while the uncompacted context still fits the window. Callers
// continue uncompacted and retry after further growth; only a context
// that reaches the window itself remains a terminal compaction failure.
var errCompactionDeferred = errors.New("compaction deferred")

// contextOccupancy is the larger of the calibrated estimate and the
// server-measured prompt of the last processed request: the estimate
// under-reads real occupancy (schemas, templating), so maintenance
// decisions trust the server whenever it reported a count.
func (r *Runner) contextOccupancy(estimate int) int {
	if r.Continuation.Measured && r.Continuation.Prompt > estimate {
		return r.Continuation.Prompt
	}
	return estimate
}

// shouldCompact compares the supplied occupancy (larger of the calibrated
// estimate and the server-measured prompt) with the YAML triggers: the
// emergency window percent. Routine pruning does not use this trigger.
func shouldCompact(lastPrompt, window int, have bool, c Compaction) bool {
	if !have || window <= 0 || c.Trigger <= 0 {
		return false
	}
	return float64(lastPrompt) >= c.Trigger/100*float64(window)
}

// summaryBudget returns the summary token budget for the nth compaction
// (0-based): Summary growing by Step, capped at Max.
func summaryBudget(c Compaction, window, n int) int {
	pct := c.Summary + c.Step*float64(n)
	if pct > c.Max {
		pct = c.Max
	}
	if b := int(pct / 100 * float64(window)); b > 0 {
		return b
	}
	return 1
}

// keepStart returns the first kept index: the latest messages whose
// apportioned content estimate fits Keep percent of the window. Both the
// candidate total and character-share boundary are estimates; the last
// measured request calibrates them but does not measure current occupancy.
// The leading system run remains outside the selection. Compact's provenance
// protection separately retains user intent, unread/current receipts and other
// obligations. An oversized unprotected latest draft may be summarized.
func keepStart(msgs []llm.Message, totalTokens int, keepPct float64, window int) int {
	return keepStartBudget(msgs, totalTokens, keepPct/100*float64(window))
}

func keepStartBudget(msgs []llm.Message, totalTokens int, budget float64) int {
	prefix := 0
	for prefix < len(msgs) && msgs[prefix].Role == "system" {
		prefix++
	}
	totalChars := 0
	for _, m := range msgs {
		totalChars += m.Size()
	}
	acc := 0.0
	start := len(msgs)
	for i := len(msgs) - 1; i >= prefix; i-- {
		share := 0.0
		if totalChars > 0 {
			share = float64(totalTokens) * float64(msgs[i].Size()) / float64(totalChars)
		}
		if acc+share > budget {
			break
		}
		acc += share
		start = i
	}
	return start
}

// wordsFor sizes the summary writing target from the token target via the YAML
// words-per-token ratio. A zero-word writing target would be meaningless.
func wordsFor(budget int, perToken float64) int {
	if w := int(float64(budget) * perToken); w > 1 {
		return w
	}
	return 1
}

// Compact is shared by automatic and manual context replacement. Only consumed,
// unprotected entries are summarized; failure leaves the original state intact.
func (r *Runner) Compact(ctx context.Context, c session.Continuation) (next session.Continuation, text string, ran bool, failure error) {
	original := c.Clone()
	if r.Prompts == nil {
		pc, e := prompts.Legacy(r.TemplatesDir)
		if e != nil {
			return original, "", false, e
		}
		r.Prompts = pc
	}
	defer func() {
		if !ran {
			next = original
		}
	}()
	c = c.Clone()
	if r.ModelKey != "" && c.ModelKey != r.ModelKey {
		c.BasisChars, c.BasisTokens = 0, 0
		c.ModelKey = r.ModelKey
		c.Invalidate()
	}
	keep := c.Protected(r.Prune.SchemasKeep)
	// Host instructions are never summary material, including legacy histories.
	for i, e := range c.Entries {
		if e.Message.Role == "system" {
			keep[i] = true
		}
	}
	ks := keepStartBudget(c.Messages(), c.Estimate(), r.Compaction.Keep/100*float64(r.MaxTokens))
	for i := 0; i < len(c.Entries); {
		end := c.GroupEnd(i)
		if i < ks && end > ks {
			ks = i
			break
		}
		i = end
	}
	if r.Compaction.Keep <= 0 {
		ks = len(c.Entries)
	}
	var b strings.Builder
	write := func(e session.Entry, reference bool) {
		provenance := "user text"
		if e.Message.Role == "assistant" || e.Kind == "summary" {
			provenance = "model text; unverified"
		} else if e.Kind == "guidance" || e.Kind == "history" && e.Round != nil && e.Round.CommentID == "" {
			provenance = "host advice; unexecuted; no authority"
		} else if e.Message.Role == "tool" || e.Kind == "output" || e.Kind == "schema" || e.Kind == "failure" || e.Kind == "evidence" {
			provenance = "recorded observation"
		}
		fmt.Fprintf(&b, "[kind=%s role=%s %s reference-only=%t]\n%s\n", e.Kind, e.Message.Role, provenance, reference, e.Message.Content)
		if e.Message.Role == "tool" {
			fmt.Fprintf(&b, "[tool receipt id=%s name=%s]\n", e.Message.ToolCallID, e.Message.Name)
		}
		for _, call := range e.Message.ToolCalls {
			fmt.Fprintf(&b, "[model tool proposal unverified id=%s name=%s]\n%s\n", call.ID, call.Name, call.Arguments)
		}
		// Model thinking is never compaction material: summaries are clean
		// factual documents built from requests, visible text, proposals
		// and recorded observations only.
	}
	for i, e := range c.Entries {
		if i < ks && !keep[i] {
			write(e, false)
		}
	}
	if b.Len() == 0 {
		return c, "", false, nil
	}
	answers := answerGroupEntries(c)
	for i, e := range c.Entries {
		if e.Message.Role == "system" {
			continue
		}
		if answers[i] {
			write(e, true)
			continue
		}
		switch e.Kind {
		case "intent", "task", "note", "answer", "failure", "evidence", "guidance":
			write(e, true)
		}
	}
	budget := summaryBudget(r.Compaction, r.MaxTokens, c.Compactions)
	scope := fmt.Sprintf("Replaceable history precedes retained entry %d of %d (zero-based). Later retained entries may be absent from this input.", ks, len(c.Entries))
	rendered, err := r.Prompts.Render("templates.compaction", map[string]any{"Words": wordsFor(budget, r.Compaction.WordsPerToken), "Range": scope})
	if err != nil {
		return c, "", false, err
	}
	if !r.ManualDecisions {
		if r.Judge == nil {
			return c, "", false, fmt.Errorf("compaction needs the selected decision judge -- context intact")
		}
		// Reserve space for the requested summary, capped at 16 KiB. This is
		// planning headroom, not a promise about model output; actual output is
		// checked again before judgment and is never trimmed.
		reserve := min(max(budget*4, 1024), 16<<10)
		if _, err := r.compactionRequest(c, b.String(), strings.Repeat("x", reserve)); err != nil {
			return c, "", false, err
		}
	}
	keyPrompt, err := r.Prompts.Render("templates.compaction", map[string]any{"Words": wordsFor(budget, r.Compaction.WordsPerToken), "Range": ""})
	if err != nil {
		return c, "", false, err
	}
	keyRaw, err := json.Marshal(struct {
		Source, Prompt, Model, Judgment string
		Config                          Compaction
		Prune                           Prune
		Workspace, Session              string
		MaxTokens                       int
		Manual                          bool
	}{b.String(), keyPrompt, r.ModelKey, r.Prompts.Text("decision.compaction.instructions"), r.Compaction, r.Prune, r.Workspace, r.SessionID, r.MaxTokens, r.ManualDecisions})
	if err != nil {
		return c, "", false, err
	}
	candidateKey := sha256.Sum256(keyRaw)
	msgs := []llm.Message{{Role: "system", Content: rendered}, {Role: "user", Content: b.String()}}
	input := c
	input.SchemaBytes = 0
	input.SchemaKey = ""
	input.Entries = nil
	for _, msg := range msgs {
		input.Entries = append(input.Entries, session.Entry{Message: msg})
	}
	if r.MaxTokens > 0 && input.Estimate() >= r.MaxTokens {
		return c, "", false, fmt.Errorf("compaction input reaches estimated %d-token bound — context intact", r.MaxTokens)
	}
	var reply string
	var u llm.Usage
	var rawReply llm.Reply
	if cached := r.compactionCandidate; cached != nil && cached.Key == candidateKey {
		if cached.Rejected {
			return c, "", false, nil
		}
		if cached.Rejection != "" {
			return c, "", false, fmt.Errorf("%s", cached.Rejection)
		}
		reply = cached.Summary
	} else {
		r.compactionCandidate = nil
		// Summaries are clean documents: the summary call never thinks.
		summaryModel := r.Model
		if client, ok := r.Model.(*llm.Client); ok {
			summaryModel = client.WithoutThinking()
		}
		// Cancellation ends generation, not recording its already received bytes.
		archiveSegment := func(segment llm.Reply, segUsage llm.Usage) error {
			if r.Session == nil {
				return nil
			}
			logCtx := context.WithoutCancel(ctx)
			log := func(k, v string) error { return r.Session.Append(logCtx, r.SessionID, k, v) }
			raw, _ := json.Marshal(segment)
			if archiveErr := nativeArchiveHeader(map[string]any{"Compaction": true, "Bytes": len(raw), "Usage": segUsage}, "compaction", log); archiveErr != nil {
				return archiveErr
			}
			return nativeArchiveFragments(raw, "compaction", log)
		}
		if model, ok := summaryModel.(ToolModel); ok {
			// Reuse streaming inactivity/cancellation without advertising tools.
			watcher := *r
			watcher.Continuation = c
			observedCtx, setupErr := watcher.thinkingContext(ctx, func(k, v string) error {
				if r.Session != nil {
					if err := r.Session.Append(ctx, r.SessionID, k, v); err != nil {
						return errors.Join(compactionRecordFailure, err)
					}
				}
				return nil
			})
			if setupErr != nil {
				return c, "", false, setupErr
			}
			// A length terminal or a stop inside an open frame is a physical
			// output boundary of the same summary generation, never a failed
			// summary: continue from the exact prefix with nothing inserted,
			// exactly as the main loop continues worker generations. Only a
			// cancelled context (user stop) ends the call early.
			// The loop below owns continuation, so the summary generation
			// must arrive through the runtime's prefix transport even though
			// it advertises no tools; the ordinary parsed stream cannot
			// return a resumable prefix on this runtime.
			callCtx := llm.WithContinuationTransport(observedCtx)
			reportedAll := true
			lastBytes := -1
			for {
				result, callErr := model.ChatTools(callCtx, msgs, nil, nil)
				rawReply = result
				reply, err = result.Message.Content, callErr
				if result.Usage.Reported {
					u.In += result.Usage.In
					u.Out += result.Usage.Out
				} else {
					reportedAll = false
				}
				if archiveErr := archiveSegment(result, result.Usage); archiveErr != nil {
					return c, "", false, errors.Join(err, compactionRecordFailure, archiveErr)
				}
				if callErr == nil || ctx.Err() != nil {
					break
				}
				resumable := result.Prefix != nil && (result.OutputLimit ||
					errors.Is(callErr, llm.ErrIncompleteNativeResponse) ||
					llm.NativeRawIncomplete(result.RawNative, result.NativeInitialState))
				if !resumable || result.Prefix.ResponseBytes() <= lastBytes {
					break
				}
				lastBytes = result.Prefix.ResponseBytes()
				callCtx = llm.WithResponseContinuation(callCtx, result)
			}
			u.Reported = reportedAll
			if err == nil && len(rawReply.Message.ToolCalls) > 0 {
				err = fmt.Errorf("compaction returned unrequested tool calls — context intact")
			}
		} else {
			reply, rawReply.Served, u, err = summaryModel.Chat(ctx, msgs)
			rawReply.Message = llm.Message{Role: "assistant", Content: reply}
			rawReply.Usage = u
			if archiveErr := archiveSegment(rawReply, u); archiveErr != nil {
				return c, "", false, errors.Join(err, compactionRecordFailure, archiveErr)
			}
		}
		if u.Reported {
			r.UsedIn += u.In
			r.UsedOut += u.Out
		} else {
			r.UsageUnknown = true
		}
		if err != nil {
			return c, "", false, fmt.Errorf("compaction summary call failed: %w", err)
		}
		if rawReply.FinishReason != "" && rawReply.FinishReason != "stop" {
			return c, "", false, fmt.Errorf("compaction summary is unfinished (finish reason %s) — context intact", rawReply.FinishReason)
		}
		if !summaryHasContent(reply) {
			reason := "compaction summary is empty or has no substantive content; context intact. Change source or compaction settings before retrying"
			r.compactionCandidate = &compactionCandidate{Key: candidateKey, Summary: reply, Rejection: reason}
			return c, "", false, fmt.Errorf("%s", reason)
		}

		r.compactionCandidate = &compactionCandidate{Key: candidateKey, Summary: reply}
	}
	// Size is never stated to the model. The limit is capacity admission
	// below: a summary that does not fit with retained context, or does
	// not reduce it, is rejected with an explicit error — never trimmed.
	summary := r.Prompts.Text("continuation.summary_prefix") + reply
	out := c
	out.Entries = nil
	inserted := false
	for i, e := range c.Entries {
		if i < ks && !keep[i] {
			if !inserted {
				out.Add("summary", "user", summary, false)
				inserted = true
			}
			continue
		}
		out.Entries = append(out.Entries, e)
	}
	// Count the actual retained messages, system text, advertised schemas and
	// summary header together. Usage.Out includes hidden summary reasoning and
	// cannot substitute for the continuation's visible-content estimate.
	estimate := out.Estimate()
	if r.MaxTokens > 0 && estimate >= r.MaxTokens {
		return c, "", false, fmt.Errorf("compaction summary plus retained context reaches estimated %d tokens (window %d) — context intact", estimate, r.MaxTokens)
	}
	if estimate >= c.Estimate() {
		// Non-reduction while the context still fits is maintenance
		// information, not a dead end: retained mass can be dominated by
		// protected groups the summary may not retire yet. Defer with the
		// candidate marked rejected so the identical context is not
		// re-summarized; the caller retries after further growth.
		if r.compactionCandidate != nil {
			r.compactionCandidate.Rejected = true
		}
		return c, "", false, fmt.Errorf("%w: compaction summary does not reduce estimated retained context (%d tokens) — context intact and still fits the window", errCompactionDeferred, estimate)
	}
	accepted, err := r.reviewCompaction(ctx, c, b.String(), reply)
	if err == nil && accepted {
		r.compactionCandidate = nil
	} else if err == nil && r.compactionCandidate != nil {
		r.compactionCandidate.Rejected = true
	} // A settled rejection is final for this source/settings candidate.
	if err != nil || !accepted {
		return c, "", false, err
	}
	out.Compactions++
	out.Invalidate()
	return out, reply, true, nil
}

// reviewCompaction checks factual anchors only; omitted history is referenced, not judged.
func (r *Runner) reviewCompaction(ctx context.Context, c session.Continuation, source, summary string) (bool, error) {
	if r.ManualDecisions {
		if r.Ask == nil {
			return false, fmt.Errorf("compaction needs your approval while decision model is disabled — context intact; use an interactive session")
		}
		prompt := "Decision model disabled — apply this summary? Original context remains saved. Reject to keep it unchanged.\n\nPROPOSED SUMMARY:\n" + summary + "\n\nSUMMARY SOURCE AND PROTECTED ANCHORS (untrusted recorded text):\n" + source
		accepted := r.Ask(ctx, prompt)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if r.Session != nil {
			raw, _ := json.Marshal(struct {
				Summary  string
				Approved bool
			}{summary, accepted})
			if err := r.Session.Append(ctx, r.SessionID, "manual-compaction", string(raw)); err != nil {
				return false, fmt.Errorf("%w: %v", compactionRecordFailure, err)
			}
		}
		return accepted, nil
	}
	if r.Judge == nil {
		return false, fmt.Errorf("compaction needs the selected decision judge — context intact")
	}
	var req decision.Request
	var v decision.Verdict
	var err error
	maxContextBytes := 0
	for admission := 0; ; admission++ {
		req, err = r.compactionRequestLimit(c, source, summary, maxContextBytes)
		if err != nil {
			return false, err
		}
		v, err = r.Judge(ctx, req)
		if err == nil {
			break
		}
		var capacity *decision.ContextCapacityError
		if !errors.As(err, &capacity) || admission >= 12 || ctx.Err() != nil {
			return false, fmt.Errorf("compaction decision unavailable; context intact: %w", err)
		}
		// Retry only a typed pre-inference refusal. Reuse the exact candidate;
		// halve optional projected history first; compactionRequestLimit may
		// then truncate only the judge-transport copy as a last resort.
		maxContextBytes = len(req.Context) / 2
		if r.Session != nil {
			if e := r.Session.Append(ctx, r.SessionID, "judgment-error", "compaction-facts admission refused before inference; retaining candidate and reducing optional projection: "+err.Error()); e != nil {
				return false, errors.Join(compactionRecordFailure, e)
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}
	resolution := "context retained; summary not applied"
	if v.Option == compactionAccept && v.Confidence > .5 {
		resolution = "accepted"
	}
	var saved error
	decision.LogJudgment(func(k, t string) {
		if saved == nil && r.Session != nil {
			saved = r.Session.Append(ctx, r.SessionID, k, t)
		}
	}, "compaction-facts", req, v, resolution, r.JudgmentContextChars)
	if saved != nil {
		return false, errors.Join(compactionRecordFailure, saved)
	}
	return resolution == "accepted", ctx.Err()
}

// References use zero-based anchor indices. Only exact repeated text is shared;
// every entry keeps its position, provenance, call ID and unread status.
type anchorContentReference struct{ Anchor, ContentFromAnchor int }

func shareAnchorContent(anchors []session.Entry) ([]session.Entry, []anchorContentReference) {
	out := make([]session.Entry, len(anchors))
	copy(out, anchors)
	first := make(map[string]int)
	var refs []anchorContentReference
	for i, e := range out {
		text := e.Message.Content
		if text == "" {
			continue
		}
		if source, ok := first[text]; ok {
			out[i].Message.Content = ""
			refs = append(refs, anchorContentReference{i, source})
		} else {
			first[text] = i
		}
	}
	return out, refs
}

// This only rejects empty/placeholder prose. Factual completeness remains the
// existing compaction decision's responsibility, never a word-count heuristic.
func summaryHasContent(text string) bool {
	boilerplate := map[string]bool{"checkpoint": true, "summary": true, "summarized": true, "context": true, "conversation": true, "compact": true, "compacted": true, "done": true, "complete": true, "completed": true, "nothing": true, "everything": true, "all": true, "the": true, "is": true, "has": true, "been": true, "was": true, "successfully": true, "work": true, "requested": true}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !boilerplate[word] {
			return true
		}
	}
	return false
}
