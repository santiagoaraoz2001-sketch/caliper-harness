package agent

import (
	"testing"

	"specific-harness/internal/tool"
)

// Judgment packets must not deny failures that rode back as tool errors.
// File tools return (Result{}, err); only bash-like tools set Result.Failed.
func TestLoopObservedFailedIncludesToolErrors(t *testing.T) {
	one := 1
	cases := []struct {
		name string
		note *observed
		want bool
	}{
		{"edit error", &observed{Tool: "edit", Error: "find text not found"}, true},
		{"read error", &observed{Tool: "read", Error: "cannot read binary file"}, true},
		{"write error", &observed{Tool: "write", Error: "expected_hash mismatch"}, true},
		{"bash result failure", &observed{Tool: "bash", Result: tool.Result{Failed: true, ExitCode: &one}}, true},
		{"nonzero exit only", &observed{Tool: "bash", Result: tool.Result{ExitCode: &one, Text: "x"}}, true},
		{"clean edit", &observed{Tool: "edit", Result: tool.Result{Text: "ok", Changed: true}}, false},
		{"clean read", &observed{Tool: "read", Result: tool.Result{Text: "ok"}}, false},
	}
	for _, tc := range cases {
		if got := loopObserved(tc.note).Failed; got != tc.want {
			t.Errorf("%s: Failed=%v, want %v", tc.name, got, tc.want)
		}
	}
}
