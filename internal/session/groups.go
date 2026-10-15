package session

import (
	"fmt"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"strings"
)

// GroupEnd is the exclusive end of one settled assistant-call/result group.
func (c Continuation) GroupEnd(i int) int {
	n := 1 + len(c.Entries[i].Message.ToolCalls)
	if i+n > len(c.Entries) {
		return len(c.Entries)
	}
	return i + n
}
func (c Continuation) validateGroups() error {
	ids := map[string]bool{}
	for i := 0; i < len(c.Entries); i++ {
		m := c.Entries[i].Message
		if m.Role == "tool" {
			return fmt.Errorf("orphan tool result")
		}
		if len(m.ToolCalls) == 0 {
			if m.ToolCallID != "" || m.Name != "" {
				return fmt.Errorf("unexpected tool association")
			}
			continue
		}
		if m.Role != "assistant" || m.Name != "" || m.ToolCallID != "" {
			return fmt.Errorf("tool calls require assistant role")
		}
		for j, call := range m.ToolCalls {
			if call.ID == "" || strings.ContainsAny(call.ID, "\r\n\t ") || call.Name == "" || ids[call.ID] {
				return fmt.Errorf("invalid or duplicate tool ID/name")
			}
			ids[call.ID] = true
			k := i + j + 1
			if k >= len(c.Entries) {
				return fmt.Errorf("unsettled tool call")
			}
			// Executed calls (output receipts) must archive with the
			// execution-grade scalar arguments that actually ran.
			// Refused calls were never executable; their bytes are
			// evidence and need only be one valid JSON object.
			if c.Entries[k].Kind == "output" {
				if _, e := tool.JSONObject(call.Arguments); e != nil {
					return e
				}
			} else if !tool.ValidJSONObject(call.Arguments) {
				return fmt.Errorf("tool call arguments are not one JSON object")
			}
			v := c.Entries[k].Message
			if v.Role != "tool" || v.ToolCallID != call.ID || v.Name != call.Name || len(v.ToolCalls) > 0 {
				return fmt.Errorf("tool receipt association mismatch")
			}
		}
		i = c.GroupEnd(i) - 1
	}
	return nil
}
func (c *Continuation) settleCrashTail() {
	for i := 0; i < len(c.Entries); i++ {
		calls := c.Entries[i].Message.ToolCalls
		if len(calls) == 0 {
			continue
		}
		for j, call := range calls {
			at := i + j + 1
			if at < len(c.Entries) && c.Entries[at].Message.Role == "tool" {
				continue
			}
			entry := Entry{Message: llm.Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: "no settled receipt; execution outcome unknown"}, Kind: "failure", Unread: true}
			c.Entries = append(c.Entries, Entry{})
			copy(c.Entries[at+1:], c.Entries[at:])
			c.Entries[at] = entry
		}
		i = c.GroupEnd(i) - 1
	}
}
