package plan

import (
	"fmt"
	"strings"
)

// ValidateApproachReady checks every task's block mechanically: all six
// fields and authored acceptance command present and non-blank.
// This is the mechanical half of granularity; judging whether a block
// REALLY needs no questions is Phase 4 (the ApproachReadyCheck hook).
func ValidateApproachReady(s stateStore, approachID string) error {
	if err := requireNoRevision(s, "approach", approachID); err != nil {
		return err
	}
	tasks, err := ApproachTasks(s, approachID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if err := requireNoRevision(s, "task", t.ID); err != nil {
			return err
		}
		if err := checkBlock(t); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTaskBlock reuses readiness's mechanical predicate before fresh preparation.
func ValidateTaskBlock(t *Task) error { return checkBlock(t) }

func checkBlock(t *Task) error {
	fields := []struct{ name, val string }{
		{"current", t.Current},
		{"target", t.Target},
		{"sketch", t.Sketch},
		{"acceptance", t.Acceptance},
		{"tests", t.Tests},
		{"verify", t.Verify},
		{"acceptance_command", t.AcceptanceCommand},
	}
	for _, f := range fields {
		if strings.TrimSpace(f.val) == "" {
			return fmt.Errorf("task %q field %q is blank — fill every block field before ready", t.ID, f.name)
		}
	}
	return nil
}
