package launch

import (
	"encoding/json"
	"strings"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

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
