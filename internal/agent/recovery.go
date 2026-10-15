package agent

import (
	"context"
	"fmt"
	"specific-harness/internal/session"
	"strings"
)

func preparationTool(name string) bool {
	return name == "graphql" || name == "prepare_task" || name == "ready_task" || name == "task_start" || strings.HasPrefix(name, "workflow_")
}

func (r *Runner) preparationReceipt(name, id, text string, executed bool) string {
	label := name + " (call " + id + "): "
	if !executed {
		return label + "unexecuted; " + text
	}
	if note := r.enforcement.current; note != nil {
		if note.Tool == "graphql" {
			receipt := label + note.Result.Text
			if note.Error != "" {
				receipt += "; actual error: " + note.Error
			}
			return receipt
		}
		if note.Error != "" || note.Result.Failed || note.Result.Blocked {
			return label + "failed/blocked; retained result: " + text
		}
	}
	return label + r.Prompts.Text("continuation.preparation.settled_authority")
}

func (r *Runner) preparationScope() string {
	if r.Assigned == nil || r.Assigned.Store == "" {
		return "" // Unknown owning identity cannot retire preparation advice.
	}
	return r.Assigned.Store + "\n" + r.Workspace
}

func (r *Runner) ownsPreparationGuidance(e session.Entry) bool {
	m := e.Message
	return e.PreparationScope != "" && e.PreparationScope == r.preparationScope() && e.Kind == "guidance" && !e.Unread && e.Round == nil &&
		m.Role == "user" && len(m.ToolCalls) == 0 && m.ToolCallID == "" && m.Name == ""
}

// Presentation uses fresh owning facts, separately from the judge's evidence and
// triggers. Only our newly tagged standalone advice is replaceable; native groups,
// legacy guidance and unresolved knowledge requests retain their provenance.
func (r *Runner) publishPreparationGuidance(ctx context.Context, advice string, prepare bool, otherAdvice string) (bool, error) {
	if advice == "" && otherAdvice == "" {
		return false, nil // Silence does not rebuild historical model-facing instructions.
	}
	if r.preparationScope() == "" || !r.PlanningActive() || r.assignedClaim {
		return false, nil
	}
	pending := r.preparationDisposition != "" || prepare
	for i := 0; !pending && i < len(r.Continuation.Entries); i = r.Continuation.GroupEnd(i) {
		pending = r.ownsPreparationGuidance(r.Continuation.Entries[i])
	}
	if !pending {
		return false, nil
	}
	value, err := r.Assigned.Facts(ctx)
	if err != nil {
		return false, fmt.Errorf("current preparation state unavailable: %w", err)
	}
	facts, ok := value.(BookkeepingFacts)
	if !ok {
		return false, nil
	}
	text := r.Prompts.Text("continuation.preparation.current_frame") + r.preparationDisposition
	if r.preparationDisposition == "" {
		text += r.Prompts.Text("continuation.preparation.historical_receipts")
	}
	if advice != "" && prepare {
		text += "\n" + advice
	}
	text += r.Prompts.Text("continuation.preparation.next_action") + facts.Preparation
	candidate := r.Continuation.Clone()
	entries := candidate.Entries
	candidate.Entries = nil
	for i := 0; i < len(entries); {
		end := r.Continuation.GroupEnd(i)
		if !r.ownsPreparationGuidance(entries[i]) {
			candidate.Entries = append(candidate.Entries, entries[i:end]...)
		}
		i = end
	}
	if otherAdvice != "" {
		candidate.AddGuidance(otherAdvice, nil)
	}
	candidate.Add("guidance", "user", text, false)
	candidate.Entries[len(candidate.Entries)-1].PreparationScope = r.preparationScope()
	candidate.Invalidate()
	raw, err := candidate.Snapshot().Encode()
	if err != nil {
		return false, err
	}
	if r.Session == nil {
		return false, fmt.Errorf("preparation recovery needs a durable session")
	}
	if err = ctx.Err(); err == nil {
		err = r.Session.Append(ctx, r.SessionID, "continuation", raw)
	}
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	r.Continuation = candidate
	return true, nil
}
