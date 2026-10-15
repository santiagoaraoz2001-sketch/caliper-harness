package outcome_test

import (
	"encoding/json"
	. "specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

type pairRecord struct {
	Kind, Command, Cwd, Started, Ended string
	Attempt                            int
	EpisodeStarted                     string `json:"episode_started,omitempty"`
	EpisodeEnded                       string `json:"episode_ended,omitempty"`
	ExitCode                           *int
	Outcome                            Outcome
	Verification                       Verification
	Acceptance                         *session.CommandReceipt `json:"acceptance,omitempty"`
}

func TestPairedCheckProjectionAndHistoricalReceipts(t *testing.T) {
	zero := 0
	base := pairRecord{Kind: "check", Command: "suite", Attempt: 2, Cwd: "/owned", Started: "suite-start", Ended: "suite-end", EpisodeStarted: "episode-start", EpisodeEnded: "episode-end", ExitCode: &zero, Outcome: Outcome{Status: Completed}, Verification: Passed, Acceptance: &session.CommandReceipt{Command: "assert behavior", Cwd: "/owned", Started: "accept-start", Ended: "accept-end", Status: "passed", ExitCode: &zero}}
	review := pairRecord{Kind: "review", Attempt: 2, Outcome: Outcome{Status: Completed}, Verification: Passed}
	for _, mode := range []string{"pair", "historical", "wrong acceptance", "wrong cwd", "missing acceptance end", "missing episode end", "nonzero", "wrong attempt", "later failed check"} {
		t.Run(mode, func(t *testing.T) {
			c := base
			a := *base.Acceptance
			c.Acceptance = &a
			switch mode {
			case "historical":
				c.Acceptance = nil
			case "wrong acceptance":
				a.Command = "invented"
			case "wrong cwd":
				a.Cwd = "/foreign"
			case "missing acceptance end":
				a.Ended = ""
			case "missing episode end":
				c.EpisodeEnded = ""
			case "nonzero":
				one := 1
				a.ExitCode = &one
			case "wrong attempt":
				c.Attempt++
			}
			records := []pairRecord{c, review}
			if mode == "later failed check" {
				latest := base
				latest.Verification = Rejected
				records = append(records, latest)
			}
			raw, _ := json.Marshal(records)
			projected := CheckProjection(string(raw))
			want := mode == "pair"
			if Checked(string(raw), "suite", 2, "assert behavior") != want || Checked(projected, "suite", 2, "assert behavior") != want {
				t.Fatalf("pair projection mismatch %s", projected)
			}
			if Checked(string(raw), "suite", 2) {
				t.Fatal("absent frozen acceptance retroclaimed")
			}
		})
	}
	// Selected projection never fetches/carries output bodies, but retains pair facts.
	raw, _ := json.Marshal([]any{map[string]any{"Kind": "check", "Command": "suite", "Attempt": 2, "Cwd": "/owned", "Started": "s", "Ended": "e", "episode_started": "s", "episode_ended": "e", "ExitCode": 0, "Outcome": map[string]string{"Status": "completed"}, "Verification": "passed", "acceptance": base.Acceptance, "Output": strings.Repeat("large", 20000)}, review})
	p := CheckProjection(string(raw))
	if len(p) > 2048 || !Checked(p, "suite", 2, "assert behavior") {
		t.Fatalf("selected paired projection %d: %s", len(p), p)
	}
}
