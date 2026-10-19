package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"specific-harness/internal/prompts"
	"strings"

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
	for _, e := range c.Entries {
		if e.Message.Role == "system" {
			continue
		}
		switch e.Kind {
		case "intent", "task", "note", "failure", "evidence", "guidance":
			write(e, true)
		}
	}
	budget := summaryBudget(r.Compaction, r.MaxTokens, c.Compactions)
	rendered, err := r.Prompts.Render("templates.compaction", map[string]any{"Words": wordsFor(budget, r.Compaction.WordsPerToken), "Range": ""})
	if err != nil {
		return c, "", false, err
	}
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
	// Summaries are clean documents: the summary call never thinks.
	summaryModel := r.Model
	if client, ok := r.Model.(*llm.Client); ok {
		summaryModel = client.WithoutThinking()
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
		result, callErr := model.ChatTools(observedCtx, msgs, nil, nil)
		rawReply = result
		reply, u, err = result.Message.Content, result.Usage, callErr
		if err == nil && len(result.Message.ToolCalls) > 0 {
			err = fmt.Errorf("compaction returned unrequested tool calls — context intact")
		}
	} else {
		reply, rawReply.Served, u, err = summaryModel.Chat(ctx, msgs)
		rawReply.Message = llm.Message{Role: "assistant", Content: reply}
		rawReply.Usage = u
	}
	if u.Reported {
		r.UsedIn += u.In
		r.UsedOut += u.Out
	} else {
		r.UsageUnknown = true
	}
	if r.Session != nil {
		// Cancellation ends generation, not recording its already received bytes.
		logCtx := context.WithoutCancel(ctx)
		log := func(k, v string) error { return r.Session.Append(logCtx, r.SessionID, k, v) }
		raw, _ := json.Marshal(rawReply)
		if archiveErr := nativeArchiveHeader(map[string]any{"Compaction": true, "Bytes": len(raw), "Usage": u}, "compaction", log); archiveErr != nil {
			return c, "", false, errors.Join(err, compactionRecordFailure, archiveErr)
		}
		if archiveErr := nativeArchiveFragments(raw, "compaction", log); archiveErr != nil {
			return c, "", false, errors.Join(err, compactionRecordFailure, archiveErr)
		}
	}
	if err != nil {
		return c, "", false, fmt.Errorf("compaction summary call failed: %w", err)
	}
	if rawReply.FinishReason != "" && rawReply.FinishReason != "stop" {
		return c, "", false, fmt.Errorf("compaction summary is unfinished (finish reason %s) — context intact", rawReply.FinishReason)
	}
	if strings.TrimSpace(reply) == "" {
		return c, "", false, fmt.Errorf("compaction summary came back empty — context intact")
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
		return c, "", false, fmt.Errorf("compaction summary does not reduce estimated retained context (%d tokens) — context intact", estimate)
	}
	if accepted, err := r.reviewCompaction(ctx, c, b.String(), reply); err != nil || !accepted {
		return c, "", false, err
	}
	out.Compactions++
	out.Invalidate()
	return out, reply, true, nil
}

// reviewCompaction checks factual anchors only; omitted history is referenced, not judged.
func (r *Runner) reviewCompaction(ctx context.Context, c session.Continuation, source, summary string) (bool, error) {
	if r.Judge == nil {
		return false, fmt.Errorf("compaction needs the selected decision judge — context intact")
	}
	anchors := []session.Entry{}
	ids := []string{}
	for _, e := range c.Entries {
		switch e.Kind {
		case "intent", "task", "note", "failure", "evidence", "guidance":
			anchors = append(anchors, e)
		}
		for _, call := range e.Message.ToolCalls {
			ids = append(ids, call.ID)
		}
	}
	raw, err := json.Marshal(struct {
		Summary       string
		Anchors       []session.Entry
		Coding        *session.CodingState
		Assignment    *session.Assignment
		Workflow      *session.WorkflowScope
		SourceSHA256  string
		SourceBytes   int
		SourceCallIDs []string
		SessionID     string
	}{summary, anchors, c.Coding, c.Assignment, c.Workflow, fmt.Sprintf("%x", sha256.Sum256([]byte(source))), len(source), ids, r.SessionID})
	if err != nil {
		return false, err
	}
	const accept = "the summary is factually consistent with essential anchors"
	req := decision.Request{Context: string(raw), Instructions: r.Prompts.Text("decision.compaction.instructions"), Options: []string{accept, "the summary contradicts, invents or omits essential facts"}}
	v, err := r.Judge(ctx, req)
	if err != nil {
		return false, fmt.Errorf("compaction decision unavailable — context intact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	resolution := "context retained; summary not applied"
	if v.Option == accept && v.Confidence > .5 {
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
