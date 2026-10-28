package launch

import (
	"context"
	"encoding/json"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestRejectedWorkerCheckCountsOneFailedTask(t *testing.T) {
	counts := countResults([]Result{{Outcome: outcome.Outcome{Status: outcome.Failed}, Verification: outcome.Rejected, State: plan.TaskFailed}})
	if counts.Failed != 1 || counts.Qualified != 0 || counts.Verified != 0 {
		t.Fatalf("one failed task counted twice: %+v", counts)
	}
}

// Missing native execution capability cannot fabricate either stage or a worker artifact.
// This covers the host-check fallback independently of worker preflight admission.
func TestHostCheckMissingShellRecordsUnexecutedEpisode(t *testing.T) {
	s, aid, ids := setup(t, 1)
	if _, err := fixtureApproachTransition(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, ids[0], plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err := plan.SetTaskAttempts(s, ids[0], 1); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	model := &outcomeModel{reply: "must not execute"}
	d := depsFor(s, model)
	d.NewRegistry = func() *tool.Registry { return tool.NewRegistry() }
	result := resultOf(task)
	result.brief = task
	verification, err := runCheck(context.Background(), d, &result, task)
	if err != nil || verification != outcome.Unverified || model.calls != 0 {
		t.Fatalf("unexecuted result=%v calls=%d err=%v", verification, model.calls, err)
	}
	var records []attemptRecord
	if err := json.Unmarshal([]byte(result.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	check := ownedAttemptRecords(t, records, "check", 1, 1)[0]
	if check.Started != "" || check.Ended != "" || check.ExitCode != nil || check.Acceptance != nil || check.Command != task.CheckCommand || !strings.Contains(check.Error, "neither episode command executed") {
		t.Fatalf("fabricated execution: %+v", check)
	}
}
