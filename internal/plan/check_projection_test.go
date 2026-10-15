package plan

import (
	"encoding/json"
	"specific-harness/internal/outcome"
	"testing"
	"time"
)

func TestCheckProjectionPreservesReceiptPredicate(t *testing.T) {
	check := map[string]any{"Kind": "check", "Command": "printf 'café\\n'", "Attempt": 3, "ExitCode": 0, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed", "Cwd": "/component", "Started": "start", "Ended": "end", "episode_started": "start", "episode_ended": "end", "acceptance": map[string]any{"command": "test 1 -eq 1", "cwd": "/component", "started": "start", "ended": "end", "exit_code": 0, "status": "passed"}}
	review := map[string]any{"Kind": "review", "Attempt": 3, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed"}
	changed := func(record map[string]any, key string, value any) map[string]any {
		copy := map[string]any{}
		for k, v := range record {
			copy[k] = v
		}
		copy[key] = value
		return copy
	}
	for _, c := range []struct {
		name    string
		records []map[string]any
		want    bool
	}{
		{"valid", []map[string]any{check, review}, true},
		{"missing pair", []map[string]any{changed(check, "acceptance", nil), review}, false},
		{"missing review", []map[string]any{check}, false},
		{"review before check", []map[string]any{review, check}, false},
		{"wrong attempt", []map[string]any{changed(check, "Attempt", 2), review}, false},
		{"wrong command", []map[string]any{changed(check, "Command", "true"), review}, false},
		{"nonzero", []map[string]any{changed(check, "ExitCode", 1), review}, false},
		{"missing exit", []map[string]any{changed(check, "ExitCode", nil), review}, false},
		{"check error", []map[string]any{changed(check, "Error", "large diagnostic"), review}, false},
		{"stopped", []map[string]any{changed(check, "Outcome", map[string]any{"Status": "stopped"}), review}, false},
		{"review rejected", []map[string]any{check, changed(review, "Verification", "failed")}, false},
		{"review error", []map[string]any{check, changed(review, "Error", "error")}, false},
		{"latest failed check", []map[string]any{check, review, changed(check, "ExitCode", 1)}, false},
		{"latest invalid review", []map[string]any{check, review, changed(review, "Attempt", 2)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(c.records)
			owner := &Task{AcceptanceCommand: "test 1 -eq 1", CheckCommand: check["Command"].(string), Attempts: 3, AttemptLog: string(raw)}
			projected, err := GraphTask(map[string]any{"id": "fixture", "created_at": time.Now(), "check_command": owner.CheckCommand, "acceptance_command": owner.AcceptanceCommand, "attempts": int64(3), "check_projection": outcome.CheckProjection(string(raw))})
			if err != nil || owner.Checked() != c.want || projected.Checked() != c.want {
				t.Fatalf("raw/projection predicate want=%v raw=%v projected=%v err=%v", c.want, owner.Checked(), projected.Checked(), err)
			}
			if c.want {
				owner.CheckCommand, projected.CheckCommand = "changed command", "changed command"
				if owner.Checked() || projected.Checked() {
					t.Fatal("stale command still checked")
				}
				owner.CheckCommand, projected.CheckCommand = check["Command"].(string), check["Command"].(string)
				owner.Attempts, projected.Attempts = 4, 4
				if owner.Checked() || projected.Checked() {
					t.Fatal("stale attempt still checked")
				}
			}
		})
	}
	for _, raw := range []string{"", "null", "[]", "PASS", `[{"Kind":"execution","Attempt":"bad"}]`} {
		if outcome.Checked(raw, "true", 3) || outcome.Checked(outcome.CheckProjection(raw), "true", 3) {
			t.Fatalf("malformed/absent receipts qualified: %q", raw)
		}
	}
}
