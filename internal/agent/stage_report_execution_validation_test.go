package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// A genuine successful report is cached by an unchanged wrapper. Its exact
// replay in the next execution must not become current qualification.
func TestStageReportRealCachedReplayDoesNotQualify(t *testing.T) {
	root := t.TempDir()
	command := `if [ -f report.json ]; then cat report.json; else printf '{"format":"caliper-test-report-v1","run_id":"%s","stages":[{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0}]}' "$CALIPER_TEST_RUN_ID" | tee report.json; fi`
	r, call := stageExecutionFixture(t, root, command)
	first, err := call(t.Context())
	if err != nil || first.Failed || !r.Continuation.Coding.Passed() {
		t.Fatalf("initial real report did not qualify: %+v %v", r.Continuation.Coding.Receipt, err)
	}
	previous := *r.Continuation.Coding.Receipt
	second, err := call(t.Context())
	next := r.Continuation.Coding.Receipt
	if err != nil || next.ExitCode == nil || *next.ExitCode != 0 {
		t.Fatalf("unchanged wrapper did not replay with exit zero: %+v %v", next, err)
	}
	if next.RunID == previous.RunID || next.Text != previous.Text || !second.Failed || r.Continuation.Coding.Passed() || next.Coverage.Satisfied() {
		t.Fatalf("cached real report became current: old=%+v new=%+v", previous, next)
	}
}

func TestStageReportCannotOverrideProcessFailureOrCancellation(t *testing.T) {
	for _, suffix := range []string{"; exit 7", "; : > stage-started; sleep 5"} {
		t.Run(suffix, func(t *testing.T) {
			command := `printf '{"format":"caliper-test-report-v1","run_id":"%s","stages":[{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0}]}' "$CALIPER_TEST_RUN_ID"` + suffix
			root := t.TempDir()
			r, call := stageExecutionFixture(t, root, command)
			var err error
			if strings.Contains(suffix, "sleep") {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				finished := make(chan error, 1)
				go func() { _, e := call(ctx); finished <- e }()
				// Cancel only after the actual command emitted its report, not
				// after an assumed process startup duration.
				for {
					if _, e := os.Stat(filepath.Join(root, "stage-started")); e == nil {
						break
					}
					select {
					case e := <-finished:
						t.Fatalf("command ended before cancellation barrier: %v", e)
					case <-ctx.Done():
						t.Fatal("command never reached cancellation barrier")
					case <-time.After(10 * time.Millisecond):
					}
				}
				cancel()
				err = <-finished
			} else {
				_, err = call(t.Context())
			}
			receipt := r.Continuation.Coding.Receipt
			if receipt == nil || r.Continuation.Coding.Passed() || receipt.Status == "passed" {
				t.Fatalf("structured pass erased process failure: %+v %v", receipt, err)
			}
			if !strings.Contains(receipt.Text, receipt.RunID) {
				t.Fatalf("fixture did not emit a current report before failure: %+v", receipt)
			}
		})
	}
}

func stageExecutionFixture(t *testing.T, root, command string) (*Runner, func(context.Context) (tool.Result, error)) {
	t.Helper()
	contract := checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"browser"}}
	c := &session.CodingState{TaskID: "stage-validation", Workspace: root, Command: command, ConfigCommand: command, Report: contract.Clone()}
	r := &Runner{Prompts: prompts.Builtin(), CheckCommand: command, CheckReport: contract, Session: &recordSink{}, SessionID: "stage-validation", Continuation: session.Continuation{Coding: c}}
	d := r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, root))
	raw, _ := json.Marshal(map[string]string{"cmd": command})
	args, repair := tool.ValidateJSON(d, string(raw))
	if repair != "" {
		t.Fatal(repair)
	}
	return r, func(ctx context.Context) (tool.Result, error) { return Direct(ctx, d, args) }
}
