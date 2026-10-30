package launch

import (
	"encoding/json"
	"strconv"
	"strings"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Point to an actual stored record; its transcript can be incomplete or compacted.
func reviewEvidenceReference(r *Result, pc *prompts.Catalog) string {
	var records []attemptRecord
	if json.Unmarshal([]byte(r.AttemptLog), &records) != nil {
		return ""
	}
	for i := len(records) - 1; i >= 0; i-- {
		x := records[i]
		if x.Kind == "execution" && x.Attempt == r.Attempts && (x.ExecutionSession != "" || x.ExecutionEvents != nil || x.Transcript != nil) {
			if !pc.Has("review.execution_evidence_reference") {
				pc = prompts.Builtin()
			}
			text, err := pc.Render("review.execution_evidence_reference", map[string]string{"TaskID": r.TaskID, "Attempt": strconv.Itoa(r.Attempts), "Record": strconv.Itoa(i)})
			if err == nil {
				return "\n" + text
			}
			return ""
		}
	}
	return ""
}

// Decompose only a known rendered template, using its original typed inputs.
// Custom/unknown instructions remain in the full advisory request. The real
// reviewer always receives the original prompt unchanged.
func reviewProgressInputs(prompt string, fields map[string]string) *agent.ReviewInputs {
	builtin, err := prompts.Builtin().Render("templates.review", fields)
	if err != nil || builtin != prompt || !json.Valid([]byte(fields["FileObservations"])) || !json.Valid([]byte(fields["CheckReceipt"])) {
		return nil
	}
	empty := make(map[string]string, len(fields))
	for key := range fields {
		empty[key] = ""
	}
	instructions, err := prompts.Builtin().Render("templates.review", empty)
	if err != nil {
		return nil
	}
	return &agent.ReviewInputs{
		Instructions: instructions, PlanContext: fields["PlanGoal"], ApproachContext: fields["ApproachTitle"],
		Title: fields["Title"], TaskRequest: fields["TaskPrompt"], Target: fields["Target"], Acceptance: fields["Acceptance"],
		Workspace: fields["Workspace"], CheckCommand: fields["CheckCommand"], AcceptanceCommand: fields["AcceptanceCommand"],
		ExecutionRequest: fields["ExecutionRequest"], Current: fields["TaskCurrent"], Sketch: fields["Sketch"],
		Tests: fields["Tests"], Verify: fields["Prompt"], HostExecutionEvidence: fields["ExecutionEvidence"],
		UnverifiedWorkerReport: fields["Current"], FileObservations: json.RawMessage(fields["FileObservations"]),
		InitialCheck: json.RawMessage(fields["CheckReceipt"]),
	}
}

// reviewFileObservations reuses settled observations, not assistant claims or
// unexecuted edit arguments. Context may retain earlier work and omit compacted
// receipts, so the template explicitly discloses both limitations.
func reviewFileObservations(messages []llm.Message, pc *prompts.Catalog) string {
	type observation struct{ CallID, Tool, Result string }
	observations := []observation{}
	calls := map[string]string{}
	textCall := ""
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			textCall = ""
			for _, call := range m.ToolCalls {
				if call.Name == "edit" || call.Name == "write" {
					calls[call.ID] = call.Name
				}
			}
			if len(m.ToolCalls) == 0 {
				call, found, repair := tool.Extract(m.Content, pc)
				if found && repair == "" && (call.Name == "edit" || call.Name == "write") {
					textCall = call.Name
				}
			}
		case "tool":
			if name, ok := calls[m.ToolCallID]; ok && m.Name == name {
				observations = append(observations, observation{m.ToolCallID, name, m.Content})
				delete(calls, m.ToolCallID)
			}
		case "user":
			if textCall != "" {
				// Text-mode invocation feedback occupies the next user-role entry.
				if strings.HasPrefix(m.Content, "tool result:\n") || strings.HasPrefix(m.Content, "tool result (truncated):\n") || strings.HasPrefix(m.Content, "tool error:") {
					observations = append(observations, observation{"", textCall, m.Content})
				}
				textCall = ""
			}
		}
	}
	encoded, _ := json.Marshal(observations)
	return string(encoded)
}
