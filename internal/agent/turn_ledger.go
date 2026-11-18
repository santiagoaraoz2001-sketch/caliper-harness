package agent

import (
	"context"
	"encoding/json"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// turnLedger publishes the Runner's one continuation. Request messages are
// snapshots made at model boundaries, never a second mutable transcript.
type turnLedger struct {
	runner *Runner
	ctx    context.Context
	Err    error
}

func (l *turnLedger) Record(kind, text string) {
	r := l.runner
	if r.Session == nil || l.Err != nil {
		return
	}
	ctx := l.ctx
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx) // Cancellation stops work, not its settlement receipt.
	}
	l.Err = r.Session.Append(ctx, r.SessionID, kind, text)
}

func (l *turnLedger) Messages() []llm.Message { return l.runner.Continuation.Messages() }

func (l *turnLedger) Add(kind, role, text string, unread bool) {
	r := l.runner
	if kind == "guidance" {
		var round *session.TaskRound
		if r.assignedClaim && r.settlement == nil {
			round = session.RoundFor(r.Continuation.Assignment, "")
		}
		r.Continuation.AddGuidance(text, round)
	} else {
		r.Continuation.Add(kind, role, text, unread)
	}
}

// Context records occupancy, not another copy of the conversation. The
// reading carries the continuation's own counters and content estimate so
// post-compaction events show retained size instead of the stale measured
// prompt alone.
func contextReadingJSON(reading session.Continuation) string {
	raw, _ := json.Marshal(struct {
		Prompt      int    `json:"prompt"`
		Measured    bool   `json:"measured"`
		ModelKey    string `json:"model_key"`
		Compactions int    `json:"compactions"`
		Revision    int    `json:"revision"`
		Estimate    int    `json:"estimate"`
	}{reading.Prompt, reading.Measured, reading.ModelKey,
		reading.Compactions, reading.Revision, reading.Estimate()})
	return string(raw)
}

func (l *turnLedger) Context(reading session.Continuation) {
	l.Record("context", contextReadingJSON(reading))
}

func (l *turnLedger) Measure(usage llm.Usage) {
	c := &l.runner.Continuation
	c.Measured = usage.Reported
	if usage.Reported {
		c.Prompt = usage.In
	}
	l.Context(*c)
}

func (l *turnLedger) Compact(next session.Continuation, summary string) error {
	encoded, err := next.Snapshot().Encode()
	if err != nil {
		return err
	}
	l.Record("compact", summary)
	l.Record("continuation", encoded)
	l.Context(next)
	if l.Err != nil {
		return l.Err
	}
	l.runner.Continuation = next
	return nil
}
