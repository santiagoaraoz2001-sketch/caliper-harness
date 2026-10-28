// Package outcome carries execution facts shared by the runner and its
// consumers. It contains no state engine: owning packages persist the facts.
package outcome

import (
	"bytes"
	"encoding/json"
	"fmt"
	"specific-harness/internal/prompts"
	"strings"
)

type Status string

const (
	Unknown   Status = "unknown"
	Completed Status = "completed"
	Stopped   Status = "stopped"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

type Verification string

const (
	Unverified  Verification = "unverified"
	Passed      Verification = "passed"
	Rejected    Verification = "failed"
	NotRequired Verification = "not_required"
)

type Outcome struct {
	Status Status
	Reason string
}

func (o Outcome) ExitCode() int {
	if o.Status == Cancelled {
		return 130
	}
	if o.Status == Completed {
		return 0
	}
	return 1
}
func Qualified(status Status, verification Verification) bool {
	return status == Completed && (verification == Passed || verification == NotRequired)
}
func Valid(status Status, verification Verification) bool {
	switch status {
	case Unknown, Completed, Stopped, Failed, Cancelled:
	default:
		return false
	}
	switch verification {
	case Unverified, Passed, Rejected, NotRequired:
	default:
		return false
	}
	// A worker can fail on a settled failing check. Retain both facts without
	// treating incomplete execution or a check pass as qualified completion.
	return status == Completed || status == Failed && verification == Rejected || verification != Passed && verification != Rejected
}

// Report identifies an invoked workflow. Only owning-state readback can mark
// it qualified; the runner's final prose cannot override an unresolved report.
type Report struct {
	Ref       string
	Qualified bool
	Outcome   Outcome
}

// checkReceipt contains only facts consumed by Checked; archived bodies stay raw.
type commandReceipt struct {
	Command, Cwd, Started, Ended, Status string
	ExitCode                             *int `json:"exit_code,omitempty"`
}
type checkReceipt struct {
	Cwd, Started, Ended  string
	EpisodeStarted       string          `json:"episode_started,omitempty"`
	EpisodeEnded         string          `json:"episode_ended,omitempty"`
	Acceptance           *commandReceipt `json:"acceptance,omitempty"`
	Kind, Command, Error string
	Attempt              int
	ExitCode             *int
	Outcome              Outcome
	Verification         Verification
}

// RevisionInvalidates prevents same-attempt receipts from surviving an explicit
// revision, even after reconciliation or a stale worker appends later output.
func RevisionInvalidates(log string, attempt int) bool {
	var records []struct {
		Kind    string
		Attempt int
	}
	if json.Unmarshal([]byte(log), &records) != nil {
		return true
	}
	for _, r := range records {
		if r.Kind == "revision_request" && r.Attempt >= attempt {
			return true
		}
	}
	return false
}

// Checked accepts only the latest relevant check and review for the exact attempt.
func Checked(log, command string, attempt int, acceptanceCommand ...string) bool {
	if strings.TrimSpace(command) == "" || len(acceptanceCommand) != 1 || strings.TrimSpace(acceptanceCommand[0]) == "" {
		return false
	}
	var records []checkReceipt
	if json.Unmarshal([]byte(log), &records) != nil {
		return false
	}
	for _, r := range records {
		if r.Kind == "revision_request" && r.Attempt >= attempt {
			return false
		}
	}
	check, review := false, false
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind == "review" && !review {
			if r.Attempt != attempt || r.Error != "" || r.Outcome.Status != Completed || r.Verification != Passed {
				return false
			}
			review = true
		}
		if r.Kind == "check" {
			a := r.Acceptance
			paired := a != nil && a.Command == acceptanceCommand[0] && a.Cwd != "" && a.Cwd == r.Cwd && a.Status == "passed" && a.Started != "" && a.Ended != "" && a.ExitCode != nil && *a.ExitCode == 0 && r.Started != "" && r.Ended != "" && r.EpisodeStarted != "" && r.EpisodeEnded != ""
			check = paired && r.Attempt == attempt && r.Command == command && r.ExitCode != nil && *r.ExitCode == 0 && r.Error == "" && r.Outcome.Status == Completed && r.Verification == Passed
			break
		}
	}
	return check && review
}

// CheckProjection preserves the scan's two relevant records, not a verdict.
// Malformed archives remain unchecked; errors retain presence, not large bodies.
func CheckProjection(log string) string {
	var records []checkReceipt
	if json.Unmarshal([]byte(log), &records) != nil {
		return "[]"
	}
	var selected []checkReceipt
	review := false
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind != "check" && (r.Kind != "review" || review) {
			continue
		}
		r.Outcome.Reason = ""
		if r.Error != "" {
			r.Error = "recorded error"
		}
		selected = append(selected, r)
		if r.Kind == "check" {
			break
		}
		review = true
	}
	if len(selected) == 2 {
		selected[0], selected[1] = selected[1], selected[0]
	}
	// Preserve the last revision barrier even when stale workers append later receipts.
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Kind == "revision_request" {
			selected = append(selected, records[i])
			break
		}
	}
	raw, _ := json.Marshal(selected) // scalar receipts cannot fail JSON encoding
	return string(raw)
}

// TaskComment is authored advice; only its append-site provenance is trusted.
var TaskCommentPrefix = prompts.Builtin().Text("continuation.task_comment_prefix")

type TaskComment struct {
	ID, TaskID, Source, SessionRef, Text, CreatedAt string
	Attempt                                         int
}

// AppendRecord preserves the bytes of existing records rather than re-encoding them.
func AppendRecord(log string, record any) (string, error) {
	raw, err := json.Marshal(record)
	if value, ok := record.(json.RawMessage); ok {
		raw = value
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(log) == "" {
		log = "[]"
	}
	body := bytes.TrimSpace([]byte(log))
	if !json.Valid(body) || len(body) < 2 || body[0] != '[' || body[len(body)-1] != ']' || !json.Valid(raw) {
		return "", fmt.Errorf("invalid task attempt log/record")
	}
	end := strings.LastIndex(log, "]")
	separator := ""
	if len(bytes.TrimSpace(body[1:len(body)-1])) > 0 {
		separator = ","
	}
	return log[:end] + separator + string(raw) + log[end:], nil
}

func TaskComments(log string, attempt int) ([]TaskComment, error) {
	if strings.TrimSpace(log) == "" {
		log = "[]"
	}
	var records []struct {
		Kind    string
		Comment *TaskComment
	}
	if err := json.Unmarshal([]byte(log), &records); err != nil {
		return nil, err
	}
	comments := []TaskComment{}
	for _, r := range records {
		if r.Kind == "comment" && r.Comment != nil && r.Comment.Attempt == attempt {
			comments = append(comments, *r.Comment)
		}
	}
	return comments, nil
}

// MergeComments keeps concurrently appended raw records, including unknown fields.
func MergeComments(log, current string) (string, error) {
	seen := map[string]bool{}
	for pass, rawLog := range []string{log, current} {
		if strings.TrimSpace(rawLog) == "" {
			rawLog = "[]"
		}
		var records []json.RawMessage
		if err := json.Unmarshal([]byte(rawLog), &records); err != nil {
			return "", err
		}
		for _, raw := range records {
			var r struct {
				Kind, ID string
				Comment  *TaskComment
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				return "", err
			}
			if r.Kind != "comment" && r.Kind != "comment_delivery" && r.Kind != "revision_request" && r.Kind != "revision_resolved" {
				continue
			}
			id := r.ID
			if r.Comment != nil {
				id = r.Comment.ID
			}
			key := r.Kind + ":" + id
			if id == "" {
				return "", fmt.Errorf("task comment record lacks identity")
			}
			if pass == 1 && !seen[key] {
				var err error
				log, err = AppendRecord(log, raw)
				if err != nil {
					return "", err
				}
			}
			seen[key] = true
		}
	}
	return log, nil
}
