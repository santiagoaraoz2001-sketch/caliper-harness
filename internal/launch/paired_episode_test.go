package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"strings"
	"testing"
)

// Only model prose/review is scripted; public launch, Kuzu and both Bash commands are real.
func TestPairedLaunchBehaviorBeforeSuite(t *testing.T) {
	for _, body := range []string{"broken", "fixed"} {
		t.Run(body, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = os.WriteFile(filepath.Join(dir, "artifact"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			ctx := plan.WithProject(context.Background(), dir)
			p, err := plan.CreatePlanContext(ctx, s, "behavior control", "artifact must contain fixed")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
				t.Fatal(err)
			}
			a, err := plan.CreateApproach(s, p.ID, "inspect actual artifact")
			if err != nil {
				t.Fatal(err)
			}
			task, err := plan.CreateTask(s, a.ID, "verify artifact", "artifact must contain fixed")
			if err != nil {
				t.Fatal(err)
			}
			acceptance := `test "$(cat artifact)" = fixed && printf ASSERTION_OBSERVED`
			suite := `printf suite >> suite-marker`
			task, err = plan.SetTaskBlock(s, task.ID, "observed artifact", "artifact equals fixed", "use actual assertion", "Command: "+acceptance+" exits0", "real artifact assertion", "inspect actual pair then verdict", suite, acceptance)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.Ready(ctx, s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
				t.Fatal(err)
			}
			m := &handoffModel{}
			d := depsFor(s, m)
			d.Workspace = dir
			d.Wf.VerifyFollowup = true
			d.AuthMode = config.AuthAll
			approved := false
			d.Approve = func(_ context.Context, prompt string) bool {
				approved = strings.Contains(prompt, acceptance) && strings.Contains(prompt, suite) && strings.Index(prompt, acceptance) < strings.Index(prompt, suite)
				if _, e := os.Stat(filepath.Join(dir, "suite-marker")); !os.IsNotExist(e) {
					t.Fatal("effect before pair approval")
				}
				return approved
			}
			childApprovals := 0
			gate := &auth.Gate{Mode: config.AuthAll, AskFunc: func(_ context.Context, prompt string) bool {
				childApprovals++
				var action struct {
					Tool string `json:"tool"`
					Args struct {
						Command    string `json:"cmd"`
						Acceptance string `json:"acceptance_cmd"`
					} `json:"args"`
				}
				parts := strings.SplitN(prompt, "Complete validated action (JSON):\n", 2)
				if len(parts) != 2 || json.Unmarshal([]byte(parts[1]), &action) != nil || !approved || action.Tool != "check" || action.Args.Command != suite || action.Args.Acceptance != acceptance {
					t.Errorf("actual child check lacks parent approval or exact complete pair: %s", prompt)
					return false
				}
				if _, e := os.Stat(filepath.Join(dir, "suite-marker")); !os.IsNotExist(e) {
					t.Fatal("effect before actual check approval")
				}
				return true
			}}
			d.ReviewGate = gate.Wrap
			d.NewRegistry = func() *tool.Registry {
				reg := tool.NewRegistry()
				reg.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
				return reg
			}
			result, err := New(d).Execute(ctx, tool.Args{"approach": a.ID})
			if err != nil {
				t.Fatal(err)
			}
			if !approved || childApprovals != 1 {
				t.Fatal("both literal commands need separate parent and actual child approval before effect")
			}
			actual, err := plan.GetTask(s, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			type pairedRecord struct {
				Kind       string
				Acceptance *session.CommandReceipt
				ExitCode   *int
			}
			var records []pairedRecord
			if err = json.Unmarshal([]byte(actual.AttemptLog), &records); err != nil {
				t.Fatal(err)
			}
			var check *pairedRecord
			for i := range records {
				if records[i].Kind == "check" {
					check = &records[i]
				}
			}
			if check == nil || check.Acceptance == nil || check.Acceptance.Command != acceptance || check.Acceptance.Cwd != dir || check.Acceptance.ExitCode == nil {
				t.Fatalf("missing actual behavior receipt: %+v", records)
			}
			if body == "broken" {
				owner := resultOf(actual)
				restored := codingFromCheck(d, &owner, actual)
				if restored.Receipt == nil || restored.Receipt.Status != "passed" || restored.Receipt.Acceptance.Status != "failed" || restored.Passed() || restored.Validate() != nil {
					t.Fatalf("readback lost independent statuses: %+v", restored.Receipt)
				}
				if *check.Acceptance.ExitCode == 0 || check.ExitCode == nil || *check.ExitCode != 0 || len(m.prompts) != 1 || actual.State != plan.TaskFailed || actual.Qualified() || !result.Failed {
					t.Fatalf("failed behavior assertion masked by suite/PASS: %+v %+v", actual, check)
				}
				if _, err = os.Stat(filepath.Join(dir, "suite-marker")); err != nil {
					t.Fatalf("independent suite did not run after failed acceptance: %v", err)
				}
				return
			}
			if !actual.Qualified() || check.ExitCode == nil || *check.ExitCode != 0 || !strings.Contains(check.Acceptance.Text, "ASSERTION_OBSERVED") {
				t.Fatalf("real paired check not qualified: %+v %+v", actual, check)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "suite-marker")); err != nil || string(b) != "suite" {
				t.Fatalf("suite execution %q %v", b, err)
			}
			review := m.prompts[len(m.prompts)-1]
			at := strings.Index(review, "current typed check facts: ")
			if at < 0 {
				t.Fatal("missing packet")
			}
			var c session.CodingState
			if err = json.Unmarshal([]byte(strings.SplitN(review[at+len("current typed check facts: "):], "\n", 2)[0]), &c); err != nil || !c.Passed() || c.AcceptanceCommand != acceptance {
				t.Fatalf("review packet missing actual pair: %+v %v", c, err)
			}
			if strings.Index(review, "ASSERTION_OBSERVED") > strings.Index(review, "worker report (unverified):") || actual.Verification != outcome.Passed {
				t.Fatal("assertion observations not ahead of prose")
			}
		})
	}
}

// A closed actual sink must fail the verification episode before either command.
func TestPairedLaunchCheckRecordingFailureStopsEffect(t *testing.T) {
	s, aid, ids := setup(t, 1)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := "touch suite-marker"
	declareFixtureCheck(t, s, aid, ids, command)
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	d := depsFor(s, &handoffModel{})
	d.Workspace = dir
	d.NewRegistry = func() *tool.Registry {
		reg := tool.NewRegistry()
		reg.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
		return reg
	}
	r := resultOf(task)
	s.Close()
	v, err := runCheck(context.Background(), d, &r, task)
	if err == nil || v != outcome.Unverified {
		t.Fatalf("recording failure masked: %s %v", v, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "suite-marker")); !os.IsNotExist(err) {
		t.Fatalf("effect after recording failure: %v", err)
	}
}

// Actual Kuzu comment and Bash/check/review sinks must preserve untouched raw
// record bytes, not just their known fields. Model review is a component double.
func TestPairedCheckAndReviewPreserveRawComment(t *testing.T) {
	s, aid, ids := setup(t, 1)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	p, e := plan.ApproachPlan(s, aid)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = plan.BindProject(t.Context(), s, p.ID, root); e != nil {
		t.Fatal(e)
	}
	if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	if _, e = plan.TransitionApproach(s, aid, plan.ApproachRunning); e != nil {
		t.Fatal(e)
	}
	if _, e = plan.TransitionTask(s, ids[0], plan.TaskRunning); e != nil {
		t.Fatal(e)
	}
	if e = plan.SetTaskAttempts(s, ids[0], 1); e != nil {
		t.Fatal(e)
	}
	if _, e = plan.AppendTaskComment(plan.WithProject(t.Context(), root), s, ids[0], 1, "user", "actual-user", "preserve β🙂 advice"); e != nil {
		t.Fatal(e)
	}
	task, e := plan.GetTask(s, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	task.AttemptLog = strings.Replace(task.AttemptLog, `"Kind":"comment"`, `"future" : [ true, { "untouched" : "β🙂" } ], "Kind":"comment"`, 1)
	if e = plan.SetTaskOutcome(s, task); e != nil {
		t.Fatal(e)
	}
	task, e = plan.GetTask(s, ids[0])
	if e != nil {
		t.Fatal(e)
	}
	var original []json.RawMessage
	if e = json.Unmarshal([]byte(task.AttemptLog), &original); e != nil || len(original) != 1 {
		t.Fatalf("raw original %s %v", task.AttemptLog, e)
	}
	raw := string(original[0])
	r := resultOf(task)
	r.Outcome.Status = outcome.Completed
	d := depsFor(s, &handoffModel{})
	d.Workspace = root
	d.NewRegistry = func() *tool.Registry {
		reg := tool.NewRegistry()
		reg.Register(bash.NewAt(config.Defaults().Tools.Bash, root))
		return reg
	}
	v, e := runCheck(t.Context(), d, &r, task)
	if e != nil || v != outcome.Passed || !strings.Contains(r.AttemptLog, raw) {
		t.Fatalf("paired check changed raw record: %v %s", e, r.AttemptLog)
	}
	r.Verification = v
	results := []Result{r}
	e = verifyPass(t.Context(), d, results, []int{0}, 0)
	r = results[0]
	if e != nil || !strings.Contains(r.AttemptLog, raw) || strings.Contains(r.AttemptLog, `"Kind":"review_pending"`) {
		t.Fatalf("review changed raw record: %v %s", e, r.AttemptLog)
	}
	if e = settle(s, &r); e != nil {
		t.Fatal(e)
	}
	current, e := plan.GetTask(s, task.ID)
	if e != nil || !current.Qualified() || !strings.Contains(current.AttemptLog, raw) {
		t.Fatalf("settlement lost exact comment bytes: %+v %v", current, e)
	}
}
