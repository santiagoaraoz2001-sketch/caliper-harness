package tui

import (
	"fmt"
	"specific-harness/internal/session"
)

func (c Chat) toolView() string {
	if c.Context.ToolView != "" {
		return c.Context.ToolView
	}
	if c.Tools == "coding" || c.Tools == "full" || c.Tools == "workflow" {
		return c.Tools
	}
	return ""
}

// Commit the existing continuation before changing the idle UI. A selection
// after the last run therefore survives closing and resuming that session.
func (m *Model) selectToolView(view string, scope *session.WorkflowScope) error {
	if view != "coding" && view != "full" && view != "workflow" || (view == "workflow") != (scope != nil) {
		return fmt.Errorf("invalid explicit tool view/scope choice")
	}
	next := m.Chat.Context.Clone()
	next.ToolView, next.Workflow = view, scope
	next.Invalidate()
	if n := len(m.Chat.SessionIDs); n > 0 && len(next.Entries) > 0 {
		raw, err := next.Snapshot().Encode()
		if err != nil {
			return fmt.Errorf("cannot save tool view: %w", err)
		}
		if err = session.Append(m.SessionsPath, m.Chat.SessionIDs[n-1], session.Event{Kind: "continuation", Text: raw}); err != nil {
			return fmt.Errorf("cannot save tool view: %w", err)
		}
	}
	m.Chat.Context, m.Chat.Tools = next, view
	m.Chat.RegistryKnown, m.Chat.RegistryNames = false, nil
	return nil
}
