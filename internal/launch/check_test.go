package launch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"strings"
	"testing"
	"time"
)

func betaCheckFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "checks"), 0700); err != nil {
		t.Fatal(err)
	}
	original := "testdata/beta-review"
	for _, file := range []string{"review_beta.py", "check_beta.py", "review_gate.json"} {
		b, err := os.ReadFile(filepath.Join(original, file))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "checks", file), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.txt"), []byte("beta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "checks/review_beta.py")
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(string(b), "declared reviewer-only approval is false") {
		t.Fatalf("original Beta control drift: %s %v", b, err)
	}
	return "cd '" + dir + "' && python3 checks/review_beta.py"
}
func TestCheckFreeformReviewerCannotQualify(t *testing.T) {
	command := betaCheckFixture(t)
	s, aid, ids := setup(t, 1)
	// Historical completed/unverified fixture retains old freeform semantics, no guessed check command.
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.check_command='',t.acceptance_command='',t.verify=$verify`, map[string]any{"id": ids[0], "verify": "Independently run " + command + " and PASS only on actual exit0"}); err != nil {
		t.Fatal(err)
	}
	// This old execution predates required-command preflight; never readmit its worker.
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.state='done',t.result='worker artifact',t.execution='completed',t.verification='unverified',t.attempts=1`, map[string]any{"id": ids[0]}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='paused'`, map[string]any{"id": aid}); err != nil {
		t.Fatal(err)
	}
	m := &outcomeModel{reply: "worker artifact", review: "PASS all checks passed"}
	d := depsFor(s, m)
	d.Wf.VerifyFollowup = true
	if _, err := Resume(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	if m.workers != 0 {
		t.Fatal("historical worker re-executed")
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if task.Qualified() || task.Verification != outcome.Unverified || task.Result != "worker artifact" {
		t.Fatalf("unchecked PASS qualified: %+v", task)
	}
}

// Calibrate only required-review fixtures to the accepted explicit-command contract.
// This emitted marker is a native mechanism control, not claimed artifact correctness.
func declareFixtureCheck(t *testing.T, s *store.Store, aid string, ids []string, command string) {
	t.Helper()
	for _, to := range []string{plan.ApproachRunning, plan.ApproachPaused} {
		if _, err := fixtureApproachTransition(s, aid, to); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		x, err := plan.GetTask(s, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = plan.SetTaskBlock(s, id, x.Current, x.Target, x.Sketch, x.Acceptance, x.Tests, x.Verify, command, x.AcceptanceCommand); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixtureApproachTransition(s, aid, plan.ApproachReady); err != nil {
		t.Fatal(err)
	}
}
func TestCheckDeclaredBetaNonzeroCannotBeOverridden(t *testing.T) {
	command := betaCheckFixture(t)
	s, aid, ids := setup(t, 1)
	declareFixtureCheck(t, s, aid, ids, command)
	m := &outcomeModel{reply: "worker artifact", review: "PASS falsely reported"}
	d := depsFor(s, m)
	d.Wf.MaxRetries = 2 // A deterministic failed check must not repeat implementation.
	// Even current verification-off cannot erase an explicit admitted command.
	if _, err := Launch(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	var records []attemptRecord
	if err = json.Unmarshal([]byte(task.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	check := ownedAttemptRecords(t, records, "check", 1, 1)[0]
	ownedAttemptRecords(t, records, "execution", 1, 1)
	ownedAttemptRecords(t, records, "execution_contract", 1, 1)
	if task.Qualified() || task.State != plan.TaskFailed || task.Execution != outcome.Failed || task.Verification != outcome.Rejected || task.Result != "worker artifact" || m.calls != 1 || task.Attempts != 1 || len(records) != 3 || check.Command != command || check.ExitCode == nil || *check.ExitCode != 1 || !strings.Contains(check.Output, "declared reviewer-only approval is false") {
		t.Fatalf("deterministic failed receipt lost: %+v records%+v calls%d", task, records, m.calls)
	}
}

func TestCheckReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"pass", "missing-tool", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, aid, ids := setup(t, 1)
			dir := t.TempDir()
			start := filepath.Join(dir, "start")
			late := filepath.Join(dir, "late")
			command := "printf CHECK_RECEIPT"
			if mode == "timeout" {
				command = "printf BEFORE; sleep 5"
			}
			if mode == "cancel" {
				command = "printf BEFORE; touch '" + start + "'; sleep 5; touch '" + late + "'"
			}
			declareFixtureCheck(t, s, aid, ids, command)
			m := &outcomeModel{reply: "worker artifact", review: "PASS"}
			d := depsFor(s, m)
			d.NewRegistry = func() *tool.Registry {
				reg := tool.NewRegistry()
				if mode != "missing-tool" {
					reg.Register(bash.New(config.BashConfig{DefaultTimeout: 1, MaxTimeout: 1, MaxBytes: 64, MaxLines: 10}))
				}
				return reg
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			joined := make(chan struct{})
			if mode == "cancel" {
				go func() {
					defer close(joined)
					deadline := time.NewTimer(4 * time.Second)
					defer deadline.Stop()
					tick := time.NewTicker(time.Millisecond)
					defer tick.Stop()
					for {
						select {
						case <-deadline.C:
							cancel()
							return
						case <-ctx.Done():
							return
						case <-tick.C:
							if _, err := os.Stat(start); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			} else {
				close(joined)
			}
			_, err := Launch(ctx, d, aid)
			<-joined
			if err != nil && mode != "cancel" {
				t.Fatal(err)
			}
			task, e := plan.GetTask(s, ids[0])
			if e != nil {
				t.Fatal(e)
			}
			var records []attemptRecord
			if e = json.Unmarshal([]byte(task.AttemptLog), &records); e != nil {
				t.Fatal(e)
			}
			if mode == "missing-tool" {
				ownedAttemptRecords(t, records, "check", task.Attempts, 0)
				execution := ownedAttemptRecords(t, records, "execution", task.Attempts, 1)[0]
				if task.Qualified() || task.Verification != outcome.Unverified || task.Result != "" || m.calls != 0 || task.Execution != outcome.Failed || !strings.Contains(execution.Error, "worker needs registered Bash") {
					t.Fatalf("missing tool fabricated execution: task=%+v calls=%d record=%+v", task, m.calls, execution)
				}
				return
			}
			check := ownedAttemptRecords(t, records, "check", task.Attempts, 1)[0]
			if check.Kind != "check" || check.Command != command || check.Attempt != task.Attempts || (mode != "missing-tool" && (check.Started == "" || check.Ended == "")) {
				t.Fatalf("receipt identity: %+v", check)
			}
			if mode == "pass" {
				if !task.Qualified() || check.ExitCode == nil || *check.ExitCode != 0 || !strings.Contains(task.VerificationReason, "configured suite passed") {
					t.Fatalf("pass evidence: %+v %+v", task, check)
				}
				return
			}
			if task.Qualified() || task.Verification != outcome.Unverified || check.ExitCode != nil || task.Result != "worker artifact" {
				t.Fatalf("incomplete check qualified/lost work: %+v %+v", task, check)
			}
			if mode == "timeout" || mode == "cancel" {
				if check.Output != "BEFORE" {
					t.Fatalf("partial bytes %q", check.Output)
				}
			}
			if mode == "cancel" {
				report, e := WorkflowReport(d, aid, err)
				if e != nil || report.Outcome.Status != outcome.Cancelled {
					t.Fatalf("cancel report %+v %v", report, e)
				}
				if _, e = os.Stat(late); !os.IsNotExist(e) {
					t.Fatalf("late effect: %v", e)
				}
			}
		})
	}
}
func TestCheckStaleSettlementRefusesAtomically(t *testing.T) {
	s, _, ids := setup(t, 1)
	old, e := plan.GetTask(s, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempts=1,t.result='newer'`, map[string]any{"id": ids[0]}); e != nil {
		t.Fatal(e)
	}
	claim := *old
	claim.Result = "stale"
	claim.Execution = outcome.Completed
	claim.Verification = outcome.Passed
	if e = plan.SettleTaskOutcome(s, &claim, plan.TaskDone, old); e == nil || !strings.Contains(e.Error(), "receipt not applied") {
		t.Fatalf("stale settlement: %v", e)
	}
	current, e := plan.GetTask(s, ids[0])
	if e != nil || current.Result != "newer" || current.State != plan.TaskQueued || current.Attempts != 1 || current.Verification == outcome.Passed {
		t.Fatalf("rollback: %+v %v", current, e)
	}
}

func TestCheckLegacyModelPassedWithoutCommandRefuses(t *testing.T) {
	s, aid, ids := setup(t, 1)
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.state='done',t.execution='completed',t.verification='passed',t.result='original artifact',t.review='PASS old'`, map[string]any{"id": ids[0]}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='complete'`, map[string]any{"id": aid}); err != nil {
		t.Fatal(err)
	}
	x, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	report, err := WorkflowReport(depsFor(s, &outcomeModel{}), aid, nil)
	if err != nil || x.Qualified() || report.Qualified || !strings.Contains(report.Outcome.Reason, "explicit rework") || x.Result != "original artifact" {
		t.Fatalf("legacy PASS trusted %+v %+v %v", x, report, err)
	}
}

func TestCheckQualificationNeedsExactCurrentReceipts(t *testing.T) {
	s, aid, ids := setup(t, 1)
	declareFixtureCheck(t, s, aid, ids, "printf CHECK")
	if _, e := Launch(context.Background(), depsFor(s, &outcomeModel{reply: "artifact", review: "PASS"}), aid); e != nil {
		t.Fatal(e)
	}
	x, e := plan.GetTask(s, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	if !x.Qualified() {
		t.Fatalf("native check refused %+v", x)
	}
	for _, change := range []string{"command", "attempt", "no-archive", "nonrequired"} {
		copy := *x
		switch change {
		case "command":
			copy.CheckCommand = "printf OTHER"
		case "attempt":
			copy.Attempts++
		case "no-archive":
			copy.AttemptLog = "[]"
		case "nonrequired":
			copy.Verification = outcome.NotRequired
		}
		if copy.Qualified() {
			t.Fatalf("%s retained false qualification", change)
		}
	}
}

func TestCheckExactCommandSharesLaunchApproval(t *testing.T) {
	s, aid, ids := setup(t, 1)
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	command := "touch '" + marker + "'"
	declareFixtureCheck(t, s, aid, ids, command)
	m := &outcomeModel{reply: "must not run"}
	d := depsFor(s, m)
	d.AuthMode = config.AuthAll
	var prompt string
	d.Approve = func(_ context.Context, p string) bool { prompt = p; return false }
	res, err := New(d).Execute(context.Background(), tool.Args{"approach": aid})
	if err == nil || !strings.Contains(prompt, command) || m.calls != 0 || res.Workflow == nil || res.Workflow.Qualified {
		t.Fatalf("approval boundary prompt%q result%+v err%v calls%d", prompt, res, err, m.calls)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("declined check effect %v", err)
	}
}
