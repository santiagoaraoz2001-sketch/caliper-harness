package tui

import "fmt"

// CloseOwnedServing releases only child processes started by this TUI.
// External services and their model residency remain unchanged on quit.
func (m Model) CloseOwnedServing() error {
	var failures []string
	if m.Shared != nil && m.Shared.Services != nil {
		if err := m.Shared.Services.Close(); err != nil {
			failures = append(failures, fmt.Sprintf("session service: %v", err))
		}
	}
	for url, p := range m.Serve.Spawned {
		if err := p.Stop(); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", url, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("owned serving cleanup: %v", failures)
	}
	return nil
}
