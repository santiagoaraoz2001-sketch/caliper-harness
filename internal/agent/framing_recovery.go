package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"specific-harness/internal/llm"
)

// recoverFraming runs only after the transport has certified a settled failed
// tool boundary. Nothing from the rejected response executes before this repair
// decision, and the returned call still passes nativeStep's normal admission.
func (r *Runner) recoverFraming(ctx context.Context, reply llm.Reply, failure *llm.ToolFramingError, log func(string, string) error) (llm.Reply, bool, error) {
	raw, err := json.Marshal(struct {
		Reply   llm.Reply
		Failure *llm.ToolFramingError
	}{reply, failure})
	if err != nil {
		return reply, false, err
	}
	if err = log("tool-framing-failure", string(raw)); err != nil {
		return reply, false, err
	}
	if err = ctx.Err(); err != nil {
		return reply, false, err
	}
	// The failing call must follow the already parsed, still unexecuted prefix.
	if failure.CallIndex != len(reply.Message.ToolCalls) {
		return reply, false, nil
	}
	def, args, explanation, ok := r.Registry.ReconstructCall(failure.Draft)
	if !ok {
		return reply, false, nil
	}
	refusal := failure.Error() + "\nRaw failed tool draft:\n" + failure.Draft + "\nDeterministic reconstruction: " + explanation
	fixed, values, selected, err := r.repairInvocation(ctx, def.Name, args, refusal, log, reply.Message)
	if err != nil || !selected {
		return reply, false, err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return reply, false, err
	}
	// IDs correlate this newly reconstructed call, never impersonate a server ID.
	id := fmt.Sprintf("recovered_%d_%d", len(r.Continuation.Entries), failure.CallIndex)
	ids := map[string]bool{}
	for _, c := range reply.Message.ToolCalls {
		ids[c.ID] = true
	}
	for ids[id] {
		id += "_repair"
	}
	call := llm.ToolCall{ID: id, Name: fixed.Name, Arguments: string(encoded)}
	mapping, _ := json.Marshal(struct {
		Failure *llm.ToolFramingError
		Call    llm.ToolCall
	}{failure, call})
	if err = log("tool-framing-reconstruction", string(mapping)); err != nil {
		return reply, false, err
	}
	reply.Message = reply.Message.Clone()
	reply.Message.ToolCalls = append(reply.Message.ToolCalls, call)
	return reply, true, nil
}
