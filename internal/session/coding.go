package session

import (
	"fmt"
	"specific-harness/internal/checkreport"
)

// CodingState is owning metadata, never inferred from model prose.
type CodingState struct {
	Report            checkreport.Contract `json:"report,omitzero"`
	Attempt           int                  `json:"attempt,omitempty"`
	TaskID            string               `json:"task_id"`
	Workspace         string               `json:"workspace"`
	Command           string               `json:"command,omitempty"`
	AcceptanceCommand string               `json:"acceptance_command,omitempty"`
	Source            string               `json:"source,omitempty"`
	ConfigCommand     string               `json:"config_command,omitempty"`
	Revision          int                  `json:"revision"`
	Closed            bool                 `json:"closed,omitempty"`
	Receipt           *CheckReceipt        `json:"receipt,omitempty"`
}

// CommandReceipt records one actually attempted command, not inferred coverage.
type CommandReceipt struct {
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Started   string `json:"started,omitempty"`
	Ended     string `json:"ended,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

func (r *CommandReceipt) Passed() bool {
	return r != nil && r.Command != "" && r.Cwd != "" && r.Started != "" && r.Ended != "" && r.Status == "passed" && r.ExitCode != nil && *r.ExitCode == 0
}

type CheckReceipt struct {
	Report         checkreport.Contract            `json:"report,omitzero"`
	RunID          string                          `json:"run_id,omitempty"`
	Coverage       checkreport.Coverage            `json:"coverage,omitzero"`
	Prerequisites  *checkreport.PrerequisiteReport `json:"prerequisites,omitempty"`
	Direct         bool                            `json:"direct,omitempty"` // New ordinary receipt, never inferred from missing acceptance.
	Text           string                          `json:"text,omitempty"`
	Truncated      bool                            `json:"truncated,omitempty"`
	Acceptance     *CommandReceipt                 `json:"acceptance,omitempty"`
	EpisodeStarted string                          `json:"episode_started,omitempty"`
	EpisodeEnded   string                          `json:"episode_ended,omitempty"`
	Command        string                          `json:"command"`
	Cwd            string                          `json:"cwd"`
	Revision       int                             `json:"revision"`
	Started        string                          `json:"started"`
	Ended          string                          `json:"ended,omitempty"`
	ExitCode       *int                            `json:"exit_code,omitempty"`
	Status         string                          `json:"status"`
	Reason         string                          `json:"reason,omitempty"`
}

func (c *CodingState) Passed() bool {
	if c == nil || c.Receipt == nil {
		return false
	}
	r := c.Receipt
	modePassed := c.AcceptanceCommand != "" && r.Acceptance.Passed() &&
		r.Acceptance.Command == c.AcceptanceCommand && r.Acceptance.Cwd == c.Workspace && !r.Direct
	if r.Direct {
		modePassed = c.Attempt == 0 && c.AcceptanceCommand == "" && r.Acceptance == nil
	}
	reportPassed := c.Report.Equal(r.Report) && r.ReportSatisfied()
	return reportPassed && modePassed &&
		r.EpisodeStarted != "" && r.EpisodeEnded != "" && r.Started != "" && r.Ended != "" &&
		r.Status == "passed" && r.ExitCode != nil && *r.ExitCode == 0 &&
		r.Command == c.Command && r.Cwd == c.Workspace && r.Revision == c.Revision
}

func (r *CheckReceipt) ReportSatisfied() bool {
	if r.Report.Validate() != nil {
		return false
	}
	if r.Report.Format == "" {
		return r.Report.IsZero()
	}
	if r.RunID == "" || !r.Coverage.Satisfied() {
		return false
	}
	return r.Report.Format != checkreport.EvidenceFormat || r.Prerequisites != nil && r.Prerequisites.Validate(r.Report, r.RunID, r.Cwd) == nil
}
func (c *CodingState) Invalidate(reason string) {
	c.Revision++
	if c.Receipt != nil {
		c.Receipt.Status = "stale"
		c.Receipt.Reason = reason
	}
}
func (c *CodingState) Historical() {
	if c != nil && c.Receipt != nil {
		c.Receipt.Status = "historical"
		c.Receipt.Reason = "prior receipt retained; rerun required"
	}
}

func (c *CodingState) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.Report.Validate(); err != nil {
		return fmt.Errorf("invalid coding report contract: %w", err)
	}
	if c.TaskID == "" || c.Workspace == "" || c.Revision < 0 {
		return fmt.Errorf("invalid coding state")
	}
	if r := c.Receipt; r != nil {
		if err := r.Report.Validate(); err != nil {
			return fmt.Errorf("invalid receipt report contract: %w", err)
		}
		if r.Command == "" || r.Cwd == "" || r.Started == "" && (r.Acceptance == nil && !r.Direct || r.ExitCode != nil || r.Status == "passed" || r.Status == "failed") || r.Revision < 0 {
			return fmt.Errorf("invalid check receipt")
		}
		if r.Direct && (c.Attempt != 0 || c.AcceptanceCommand != "" || r.Acceptance != nil || r.EpisodeStarted == "") {
			return fmt.Errorf("direct receipt conflicts with assigned verification")
		}
		if a := r.Acceptance; a != nil {
			if a.Command == "" || a.Cwd != r.Cwd || r.EpisodeStarted == "" {
				return fmt.Errorf("invalid acceptance receipt")
			}
			switch a.Status {
			case "unexecuted", "incomplete", "failed", "unverified":
			case "passed":
				if !a.Passed() {
					return fmt.Errorf("invalid passed acceptance receipt")
				}
			default:
				return fmt.Errorf("invalid acceptance status")
			}
		}
		switch r.Status {
		case "incomplete", "failed", "stale", "historical", "unverified", "unexecuted":
		case "passed":
			// This status belongs to the configured suite. Acceptance can fail
			// independently; aggregate qualification remains CodingState.Passed.
			suitePassed := r.ExitCode != nil && *r.ExitCode == 0 && r.Command == c.Command && r.Cwd == c.Workspace && r.Revision == c.Revision
			if !c.Report.Equal(r.Report) || !r.ReportSatisfied() {
				return fmt.Errorf("passed receipt lacks declared stage evidence")
			}
			if !suitePassed || r.Ended == "" || r.Acceptance != nil && (r.EpisodeStarted == "" || r.EpisodeEnded == "" || r.Acceptance.Command != c.AcceptanceCommand) {
				return fmt.Errorf("invalid passed check receipt")
			}
		default:
			return fmt.Errorf("invalid check status")
		}
	}
	return nil
}

// Assignment is explanatory provenance; restoring it never renews execution authority.
type Assignment struct {
	Store, Workspace, PlanID, ApproachID, TaskID string
	Attempt                                      int
}
