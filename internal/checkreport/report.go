// Package checkreport interprets declared structured execution reports, never model claims.
package checkreport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const StageFormat = "caliper-test-report-v1"
const EvidenceFormat = "caliper-test-report-v2"

type Coverage struct {
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	Expected        int    `json:"expected"`
	Discovered      int    `json:"discovered"`
	Executed        int    `json:"executed"`
	Passed          int    `json:"passed"`
	Failed          int    `json:"failed"`
	Skipped         int    `json:"skipped"`
	SkippedPackages int    `json:"skipped_packages"`
}

func (c Coverage) Satisfied() bool { return c.Status == "matched" || c.Status == "observed" }

// Evaluate consumes only this command's captured output. There is no file lookup,
// log-line scanning, inferred coverage or protection against a deliberately lying test.
func Evaluate(c Contract, command, runID, text string, truncated bool, started, ended string, host ...PrerequisiteReport) Coverage {
	unknown := Coverage{Status: "unknown", Expected: len(c.Expected), Reason: "opaque command; process success does not establish semantic coverage"}
	if err := c.Validate(); err != nil {
		unknown.Reason = err.Error()
		return unknown
	}
	if c.Format == "" {
		return unknown
	}
	if c.Format == "go-test-json" {
		if !UncachedGoTest(command) {
			unknown.Reason = "current coverage requires a direct go test -json -count=1 command; wrapper/cached execution is unsupported"
			return unknown
		}
		return GoCoverage(text, truncated, c.Expected, started, ended)
	}
	unknown.Reason = "missing, truncated or malformed current stage report"
	if truncated || runID == "" {
		return unknown
	}
	var report struct {
		Format string `json:"format"`
		RunID  string `json:"run_id"`
		Stages []struct {
			ID         string                `json:"id"`
			Status     string                `json:"status"`
			Discovered int                   `json:"discovered"`
			Executed   int                   `json:"executed"`
			Failed     int                   `json:"failed"`
			Skipped    int                   `json:"skipped"`
			Evidence   []EvidenceObservation `json:"evidence,omitempty"`
		} `json:"stages"`
	}
	if err := uniqueJSON(json.NewDecoder(strings.NewReader(text))); err != nil {
		return unknown
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&report); err != nil {
		return unknown
	}
	if report.Format != c.Format || report.RunID != runID {
		unknown.Reason = "report does not belong to this admitted check execution"
		return unknown
	}
	out := Coverage{Status: "incomplete", Expected: len(c.Expected), Reason: "required stages are absent, skipped, failed or unexecuted"}
	seen := map[string]string{}
	requirements := map[string]StageRequirement{}
	for _, requirement := range c.Stages {
		requirements[requirement.ID] = requirement
	}
	for _, stage := range report.Stages {
		if c.Format == StageFormat && stage.Evidence != nil {
			out.Reason = "stage evidence requires caliper-test-report-v2"
			return out
		}
		if strings.TrimSpace(stage.ID) == "" || seen[stage.ID] != "" || stage.Discovered < 0 || stage.Executed < 0 || stage.Failed < 0 || stage.Skipped < 0 || stage.Failed > stage.Executed || stage.Skipped > stage.Discovered || stage.Executed > stage.Discovered-stage.Skipped {
			out.Reason = "duplicate stage or contradictory counts"
			return out
		}
		switch stage.Status {
		case "passed":
			if stage.Executed == 0 || stage.Failed != 0 || stage.Skipped != 0 || stage.Executed != stage.Discovered {
				out.Reason = "passed stage has incomplete or contradictory counts"
				return out
			}
		case "failed":
			if stage.Failed == 0 {
				out.Reason = "failed stage has no failed execution"
				return out
			}
		case "skipped":
			if stage.Executed != 0 || stage.Skipped == 0 {
				out.Reason = "skipped stage has contradictory counts"
				return out
			}
		default:
			out.Reason = "unknown stage status"
			return out
		}
		if stage.Discovered > int(^uint(0)>>1)-out.Discovered {
			out.Reason = "aggregate stage counts overflow"
			return out
		}
		seen[stage.ID] = stage.Status
		out.Discovered += stage.Discovered
		out.Executed += stage.Executed
		out.Failed += stage.Failed
		out.Skipped += stage.Skipped
		out.Passed += stage.Executed - stage.Failed
		if c.Format == EvidenceFormat {
			if err := matchEvidence(requirements[stage.ID], stage.Evidence); err != nil {
				out.Reason = fmt.Sprintf("stage %q: %v", stage.ID, err)
				return out
			}
		}
	}
	for _, id := range c.Expected {
		if seen[id] != "passed" {
			status := seen[id]
			if status == "" {
				status = "missing"
			}
			out.Reason = fmt.Sprintf("required stage %q did not execute successfully (recorded %s)", id, status)
			return out
		}
	}
	if out.Executed == 0 || out.Failed > 0 {
		out.Reason = "structured report has no executed tests or includes failed execution"
		return out
	}
	if c.Format == EvidenceFormat {
		if len(host) != 1 {
			out.Status, out.Reason = "unknown", "missing current host prerequisite observations"
			return out
		}
		if err := host[0].Validate(c, runID, host[0].Workspace); err != nil {
			out.Status, out.Reason = "unknown", err.Error()
			return out
		}
	}
	out.Status, out.Reason = "matched", "declared stages executed successfully in this check; semantic quality remains a review judgment"
	return out
}

// Refuse duplicate keys and trailing envelopes before typed decoding. This is
// JSON syntax validation, not a report-language parser or an execution engine.
func uniqueJSON(d *json.Decoder) error {
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		keys := map[string]bool{}
		for d.More() {
			if delim == '{' {
				key, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				keys[name] = true
			}
			if err := value(); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
