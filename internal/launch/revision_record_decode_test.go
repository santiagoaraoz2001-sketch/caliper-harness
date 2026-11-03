package launch

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/pathutil"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

// revisionFirstLog mirrors plan.appendTaskRevision: the first record carries a
// Revision object under the same JSON key attemptRecord uses for its int
// counter. A task that ever received a revision request has this shape.
func revisionFirstLog(t *testing.T) string {
	t.Helper()
	rev := plan.Revision{ID: "rev-1", Kind: "revision_request", TargetKind: "task", TargetID: "task-1", Source: "model-authored", Text: "narrow scope", TaskIDs: []string{"task-1"}}
	log, err := outcome.AppendRecord("", struct {
		Kind, ID string
		Attempt  int
		Revision plan.Revision
	}{rev.Kind, rev.ID, 1, rev})
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func TestRecordCurrentCheckToleratesRevisionRecord(t *testing.T) {
	cwd, err := pathutil.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	task := &plan.Task{ID: "task-1", CheckCommand: "suite", AcceptanceCommand: "accept"}
	r := &Result{Attempts: 1, AttemptLog: revisionFirstLog(t)}
	c := &session.CodingState{
		TaskID: "task-1", Attempt: 1, Workspace: cwd, Command: "suite", AcceptanceCommand: "accept", Revision: 3,
		Receipt: &session.CheckReceipt{
			Command: "suite", Cwd: cwd, Revision: 3,
			Started: "2026-11-03T12:18:09Z", Ended: "2026-11-03T12:18:12Z",
			EpisodeStarted: "2026-11-03T12:18:09Z", EpisodeEnded: "2026-11-03T12:18:12Z",
			Status: "passed", ExitCode: &zero,
			Acceptance: &session.CommandReceipt{Command: "accept", Cwd: cwd, Started: "2026-11-03T12:18:09Z", Ended: "2026-11-03T12:18:10Z", Status: "passed", ExitCode: &zero},
		},
	}
	v, err := recordCurrentCheck(Deps{Workspace: cwd}, r, task, c)
	if err != nil {
		t.Fatalf("settlement must not fail on a foreign revision record: %v", err)
	}
	if v != outcome.Passed {
		t.Fatalf("verification = %s, want passed", v)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(r.AttemptLog), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2 || !strings.Contains(string(raw[0]), `"Revision":{`) {
		t.Fatalf("foreign record bytes must be retained first: %s", r.AttemptLog)
	}
	var records []attemptRecord
	if err := json.Unmarshal([]byte(r.AttemptLog), &records); err != nil {
		t.Fatalf("log must stay decodable after recording the check: %v", err)
	}
	if records[0].Kind != "revision_request" || records[0].Revision != 0 {
		t.Fatalf("foreign record decoded as %+v", records[0])
	}
	if records[1].Kind != "check" || records[1].Revision != 3 {
		t.Fatalf("check record lost its numeric revision: %+v", records[1])
	}
}

func TestCodingFromCheckToleratesRevisionRecord(t *testing.T) {
	cwd, err := pathutil.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	log, err := outcome.AppendRecord(revisionFirstLog(t), attemptRecord{
		Kind: "check", Attempt: 1, Command: "suite", Cwd: cwd, Revision: 7,
		Started: "2026-11-03T12:18:09Z", Ended: "2026-11-03T12:18:12Z",
		EpisodeStarted: "2026-11-03T12:18:09Z", EpisodeEnded: "2026-11-03T12:18:12Z",
		ExitCode: &zero, Verification: outcome.Passed, Outcome: outcome.Outcome{Status: outcome.Completed},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := &plan.Task{ID: "task-1", CheckCommand: "suite", AcceptanceCommand: "accept"}
	r := &Result{Attempts: 1, AttemptLog: log}
	c := codingFromCheck(Deps{Workspace: cwd}, r, task)
	if c.Receipt == nil {
		t.Fatal("stored check receipt must survive a preceding revision record")
	}
	if c.Revision != 7 || c.Receipt.Revision != 7 || c.Receipt.Status != "passed" {
		t.Fatalf("numeric revision must round-trip unchanged: coding=%d receipt=%d status=%s", c.Revision, c.Receipt.Revision, c.Receipt.Status)
	}
}
