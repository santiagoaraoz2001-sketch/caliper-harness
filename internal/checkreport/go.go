package checkreport

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// goTestCoverage consumes only this command's captured go test -json stream.
// It never opens a possibly stale report or interprets arbitrary PASS/SKIP text.
// The structured report establishes execution counts, not semantic test quality.
func GoCoverage(text string, truncated bool, expected []string, started, ended string) Coverage {
	out := Coverage{Status: "unknown", Expected: len(expected)}
	start, startErr := time.Parse(time.RFC3339Nano, started)
	end, endErr := time.Parse(time.RFC3339Nano, ended)
	if startErr != nil || endErr != nil || end.Before(start) {
		out.Reason = "command interval unavailable; structured output cannot be bound to this run"
		return out
	}
	if truncated {
		out.Reason = "structured output was truncated; completeness unknown"
		return out
	}
	tests, packages := map[string]string{}, map[string]string{}
	seenEvent := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e struct {
			Time    time.Time
			Action  string
			Package string
			Test    string
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Package == "" || e.Action == "" {
			out.Reason = "missing or malformed go-test-json output; completeness unknown"
			return out
		}
		if e.Time.IsZero() || e.Time.Before(start) || e.Time.After(end) {
			out.Reason = "structured event timestamp is outside this command; historical or unbound report rejected"
			return out
		}
		seenEvent = true
		key := e.Package + "/" + e.Test
		if _, ok := packages[e.Package]; !ok {
			packages[e.Package] = "started"
		}
		switch e.Action {
		case "start", "output", "pause", "cont":
		case "run":
			if e.Test == "" || tests[key] != "" {
				out.Reason = "duplicate or invalid test run event; use a single non-repeated go test -json execution"
				return out
			}
			tests[key] = "running"
		case "pass", "fail", "skip":
			if e.Test == "" {
				if packages[e.Package] != "started" {
					out.Reason = "duplicate package completion in structured output"
					return out
				}
				packages[e.Package] = e.Action
			} else {
				if tests[key] != "running" {
					out.Reason = "test completion lacks its current run event"
					return out
				}
				tests[key] = e.Action
			}
		default:
			out.Reason = fmt.Sprintf("unsupported go-test-json action %q; completeness unknown", e.Action)
			return out
		}
	}
	if !seenEvent {
		out.Reason = "structured report missing; no tests established"
		return out
	}
	out.Discovered = len(tests)
	for _, status := range tests {
		switch status {
		case "pass":
			out.Executed++
			out.Passed++
		case "fail":
			out.Executed++
			out.Failed++
		case "skip":
			out.Skipped++
		}
	}
	out.Status = "incomplete"
	for _, status := range packages {
		if status == "skip" {
			out.SkippedPackages++
		}
	}
	if out.SkippedPackages != 0 {
		out.Reason = "selected packages were skipped; executed test counts do not cover those packages"
		return out
	}
	for name, status := range packages {
		if status == "started" || status == "fail" {
			out.Reason = "package did not complete successfully: " + name
			return out
		}
	}
	for name, status := range tests {
		if status == "running" {
			out.Reason = "test has no completion: " + name
			return out
		}
	}
	if out.Executed == 0 {
		out.Reason = "zero executed tests; process success does not establish coverage"
		return out
	}
	if out.Skipped != 0 || out.Failed != 0 {
		out.Reason = "failed or skipped tests remain in the selected scope"
		return out
	}
	for _, name := range expected {
		if tests[name] != "pass" {
			out.Reason = "expected test did not pass in this execution: " + name
			return out
		}
	}
	out.Status, out.Reason = "observed", "structured execution observed; semantic coverage unknown"
	if len(expected) != 0 {
		out.Status, out.Reason = "matched", "declared expected tests passed; semantic quality remains a review judgment"
	}
	return out
}

// Keep the adapter contract narrow. This is format eligibility, not shell
// authorization or protection against deliberately fabricated test output.
func UncachedGoTest(command string) bool {
	if strings.ContainsAny(command, ";&|<>`\r\n") || strings.Contains(command, "$(") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) < 4 || fields[0] != "go" || fields[1] != "test" {
		return false
	}
	jsonMode, uncached := false, false
	for _, arg := range fields[2:] {
		if arg == "-json" {
			jsonMode = true
		}
		if strings.HasPrefix(arg, "-count") {
			if arg != "-count=1" {
				return false
			}
			uncached = true
		}
	}
	return jsonMode && uncached
}
