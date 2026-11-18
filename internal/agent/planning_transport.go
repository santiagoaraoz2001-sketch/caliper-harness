package agent

import (
	"encoding/json"
	"specific-harness/internal/llm"
	"strings"
)

func (r *Runner) currentPlanningQuestion() string {
	if r.Registry != nil && r.PlanningActive() && (!r.assignedClaim || r.settlement != nil) {
		if def, ok := r.Registry.Lookup("plan"); ok {
			return def.Schema()
		}
	}
	return ""
}

// The client cannot infer host provenance from arbitrary message text. Only an
// exact host schema entry or a correlated successful plan receipt qualifies.
func (r *Runner) planningPublished(question string) bool {
	if question == "" {
		return false
	}
	for _, e := range r.Continuation.Entries {
		if e.Kind == "schema" && e.Message.Role == "user" && e.Message.Content == r.workflowHeader+"\n"+question {
			return true
		}
	}
	return r.planningReceiptPublished(question)
}

func (r *Runner) planningReceiptPublished(question string) bool {
	if question == "" {
		return false
	}
	entries := r.Continuation.Entries
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		m := e.Message
		if e.Kind != "output" || m.Role != "tool" || m.Name != "plan" || m.ToolCallID == "" {
			continue
		}
		body, successful := strings.CutPrefix(m.Content, "tool result:\n")
		if !successful {
			continue
		}
		var result struct {
			Next    string   `json:"next_question"`
			Errors  []string `json:"errors"`
			Omitted bool     `json:"data_omitted"`
		}
		if json.Unmarshal([]byte(body), &result) != nil || result.Next != question || len(result.Errors) != 0 || result.Omitted {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			previous := entries[j].Message
			if previous.Role == "tool" {
				continue
			}
			if previous.Role == "assistant" {
				for _, call := range previous.ToolCalls {
					if call.ID == m.ToolCallID && call.Name == "plan" {
						return true
					}
				}
			}
			break
		}
	}
	return false
}

func (r *Runner) ensurePlanningPublication() bool {
	question := r.currentPlanningQuestion()
	body := r.workflowHeader + "\n" + question
	receipt, retained, changed := r.planningReceiptPublished(question), false, false
	// Retire superseded owned frames in place instead of deleting them:
	// the content bytes stay exact, so the provider's cached prompt prefix
	// survives; only the kind stops counting as a current publication.
	// Deleting an early frame rewrote the whole suffix after it and forced
	// a full re-prefill on every planning transition.
	for i := range r.Continuation.Entries {
		e := &r.Continuation.Entries[i]
		if e.Kind == "schema" && r.ownsPreparationPublication(*e) {
			if receipt || question == "" || e.Message.Content != body || retained {
				e.Kind = "history"
				changed = true
				continue
			}
			retained = true // An unchanged current frame stays in its original place.
		}
	}
	if question != "" && !r.planningPublished(question) {
		// Existing schema retention protects this current publication. Its tag
		// permits replacement after a real owner/view transition, not authority.
		r.Continuation.Add("schema", "user", body, false)
		r.Continuation.Entries[len(r.Continuation.Entries)-1].PreparationScope = r.preparationScope()
		changed = true
	}
	if changed {
		r.Continuation.Invalidate()
	}
	return changed
}

func (r *Runner) planningPlacement(defs []llm.ToolDefinition) {
	provider, ok := r.Model.(interface{ LatePlanningSchema() bool })
	eligible := ok && provider.LatePlanningSchema() && len(defs) > 1 && r.planningPublished(r.currentPlanningQuestion())
	for i := range defs {
		defs[i].TailPublished = eligible && defs[i].Function.Name == "plan"
	}
}
