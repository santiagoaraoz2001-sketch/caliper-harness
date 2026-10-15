package tui

import (
	"context"
	"encoding/json"
	"os"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/launch"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestOutcomeUICountsAndDetails(t *testing.T) {
	a := &plan.Approach{ID: "a", Title: "mixed", State: plan.ApproachRunning}
	tasks := []*plan.Task{
		{ID: "pass", Title: "pass", State: plan.TaskDone, Execution: outcome.Completed, Verification: outcome.Passed},
		{ID: "unverified", Title: "unverified", State: plan.TaskDone, Execution: outcome.Completed, Verification: outcome.Unverified, Result: "retained output", VerificationReason: "review unavailable", Review: "raw review", AttemptLog: `[]`},
		{ID: "legacy", Title: "legacy", State: plan.TaskDone, Execution: outcome.Unknown, Verification: outcome.Unverified},
		{ID: "fail", Title: "fail", State: plan.TaskFailed, Execution: outcome.Completed, Verification: outcome.Rejected},
		{ID: "stop", Title: "stop", State: plan.TaskStopped, Execution: outcome.Stopped, Verification: outcome.Unverified},
		{ID: "cancel", Title: "cancel", State: plan.TaskCancelled, Execution: outcome.Cancelled, Verification: outcome.Unverified},
	}
	// Actual paired Bash execution; reviewer is a labelled component receipt, not behavioral coverage.
	command, acceptanceCommand := "printf UI_CHECK", "printf UI_ACCEPTANCE"
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	episodeStart := time.Now().Format(time.RFC3339Nano)
	acceptance, err := bash.New(config.Defaults().Tools.Bash).Execute(context.Background(), tool.Args{"cmd": acceptanceCommand})
	if err != nil || acceptance.ExitCode == nil || *acceptance.ExitCode != 0 {
		t.Fatalf("fixture acceptance %+v %v", acceptance, err)
	}
	acceptanceEnd := time.Now().Format(time.RFC3339Nano)
	res, err := bash.New(config.Defaults().Tools.Bash).Execute(context.Background(), tool.Args{"cmd": command})
	if err != nil || res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("fixture check %+v %v", res, err)
	}
	episodeEnd := time.Now().Format(time.RFC3339Nano)
	tasks[0].CheckCommand, tasks[0].AcceptanceCommand, tasks[0].Attempts = command, acceptanceCommand, 1
	b, err := json.Marshal([]map[string]any{
		{"Kind": "check", "Command": command, "Cwd": cwd, "Attempt": 1, "Started": acceptanceEnd, "Ended": episodeEnd, "episode_started": episodeStart, "episode_ended": episodeEnd, "acceptance": map[string]any{"command": acceptanceCommand, "cwd": cwd, "started": episodeStart, "ended": acceptanceEnd, "exit_code": acceptance.ExitCode, "status": "passed"}, "ExitCode": res.ExitCode, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed},
		{"Kind": "review", "Attempt": 1, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Passed, "Output": "PASS component fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks[0].AttemptLog = string(b)
	unverified := tasks[1]
	row := wfRow("P", a, tasks)
	if row.Done != 1 || row.Total != 6 || row.Executed != 3 || row.Verified != 1 || row.Unverified != 2 || row.Failed != 1 || row.Stopped != 1 || row.Cancelled != 1 {
		t.Fatalf("UI actual mixed counts=%+v", row)
	}
	m := New(testCfg(), "component")
	m.Plans = PlansState{Plans: []LoadedPlan{{P: &plan.Plan{ID: "p", Title: "P"}, Approaches: []LoadedApproach{{A: a, Tasks: tasks}}}}, Loaded: true}
	m.Plans.rebuild("")
	if m.Plans.Rows[0].Done != 1 || m.Plans.Rows[1].Done != 1 {
		t.Fatalf("plans success counts=%+v", m.Plans.Rows)
	}
	detail := strings.Join(detailLogical(&PlanDetail{T: unverified, Ref: "A1-T2"}), "\n")
	for _, want := range []string{"execution=completed verification=unverified qualified=false", "retained output", "review unavailable", "raw review"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks%q: %s", want, detail)
		}
	}
}

func TestOutcomeWorkflowDoneDoesNotAnnounceUnqualifiedSuccess(t *testing.T) {
	m := New(testCfg(), "component")
	m.Wf.Busy = "a"
	m.onWfDone(wfDoneMsg{approach: "a", text: workflowCounts([]launch.Result{{State: plan.TaskDone, Outcome: outcome.Outcome{Status: outcome.Completed}, Verification: outcome.Unverified}}), report: &outcome.Report{Ref: "a", Outcome: outcome.Outcome{Status: outcome.Failed, Reason: "required review unavailable"}}})
	if m.Wf.Busy != "" || len(m.Alerts) == 0 {
		t.Fatalf("completion not presented: %+v", m)
	}
	alert := m.Alerts[len(m.Alerts)-1]
	if !strings.Contains(alert.Text, "0/1 qualified") || !strings.Contains(alert.Text, "required review unavailable") {
		t.Fatalf("false completion alert: %+v", alert)
	}
}

func TestOutcomeChatManagedStopShown(t *testing.T) {
	m := New(testCfg(), "component")
	m.Chat.Running = true
	m.Chat.Seq = 1
	m.Chat.LastFull = "prompt"
	m.onRunDone(runDoneMsg{seq: 1, final: "retained final", outcome: outcome.Outcome{Status: outcome.Stopped, Reason: "budget exhausted"}})
	var text strings.Builder
	for _, line := range m.Chat.Lines {
		text.WriteString(line.Text)
	}
	if !strings.Contains(text.String(), "run stopped: budget exhausted") || !strings.Contains(text.String(), "retained final") {
		t.Fatalf("outcome/raw lost: %s", text.String())
	}
}

func TestOutcomeLegacyCompleteVisibleInWorkflows(t *testing.T) {
	m := wfModel(t)
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach)-[:HAS_TASK]->(x:Task) WHERE a.title='C-done' SET x.execution=NULL,x.verification=NULL`, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	m = loadWf(m)
	if m.Wf.Err != "" || len(m.Wf.Rows) != 3 || m.Wf.Rows[2].Title != "C-done" || m.Wf.Rows[2].State != plan.ApproachComplete || m.Wf.Rows[2].Done != 0 {
		t.Fatalf("legacy complete hidden/qualified: %+v", m.Wf)
	}
}

func TestOutcomeUIRejectsUngroundedPassedCount(t *testing.T) {
	x := &plan.Task{State: plan.TaskDone, Execution: outcome.Completed, Verification: outcome.Passed, CheckCommand: "printf CLAIM"}
	row := wfRow("p", &plan.Approach{}, []*plan.Task{x})
	if row.Done != 0 || row.Verified != 0 || row.Executed != 1 {
		t.Fatalf("receipt-free PASS visible as verified %+v", row)
	}
}
