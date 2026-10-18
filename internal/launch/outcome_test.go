package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

type outcomeModel struct {
	reply, review  string
	reviewErr      error
	calls, workers int
	partial        string
	failFirst      bool
}

func (m *outcomeModel) Chat(_ context.Context, tx []llm.Message) (string, string, llm.Usage, error) {
	m.calls++
	if strings.HasPrefix(fixtureRoleRequest(tx), "Review ") {
		return m.review, "component", llm.Usage{}, m.reviewErr
	}
	m.workers++
	if m.failFirst && m.workers == 1 {
		return m.partial, "component", llm.Usage{}, errors.New("actual worker interruption")
	}
	return m.reply, "component", llm.Usage{}, nil
}

// Scripted model; actual registry, Bash, reviewer Runner and Kuzu settlement.
type reviewReasonModel struct {
	final string
	calls int
}

func (m *reviewReasonModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	m.calls++
	switch m.calls {
	case 1:
		return "completed artifact", "component", llm.Usage{}, nil
	case 2:
		return "```tool\ntool: check\ncmd: printf CHECK_RECEIPT\n```", "component", llm.Usage{}, nil
	default:
		return m.final, "component", llm.Usage{}, nil
	}
}

func TestReviewCauseSurvivesFreshReviewerCheck(t *testing.T) {
	for _, tc := range []struct {
		name, final, cause string
		verification       outcome.Verification
	}{
		{"marker-before-PASS", "R26_BEHAVIOR_α\nPASS: actual check", "review verdict pending; step budget exhausted", outcome.Unverified},
		{"FAIL", "FAIL: observed unmet acceptance", "model reported " + string(outcome.Rejected), outcome.Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, aid, ids := setup(t, 1)
			declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
			m := &reviewReasonModel{final: tc.final}
			d := depsFor(s, m)
			d.Workspace, d.Wf.VerifyFollowup = bindFixtureProject(t, s, aid, t.TempDir()), true
			if _, err := Launch(context.Background(), d, aid); err != nil {
				t.Fatal(err)
			}
			x, err := plan.GetTask(s, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 3
			if tc.name == "marker-before-PASS" {
				wantCalls = 1 + d.MaxSteps
			}
			if m.calls != wantCalls || x.Qualified() || x.Verification != tc.verification || x.Review != tc.final || !strings.Contains(x.VerificationReason, tc.cause) {
				t.Fatalf("computed review cause lost: calls=%d task=%+v", m.calls, x)
			}
			var records []attemptRecord
			if err := json.Unmarshal([]byte(x.AttemptLog), &records); err != nil {
				t.Fatal(err)
			}
			checks := 0
			for _, record := range records {
				if record.Kind == "check" && record.Command == "printf CHECK_RECEIPT" && record.ExitCode != nil && *record.ExitCode == 0 {
					checks++
				}
			}
			if checks != 2 || len(records[len(records)-1].ReviewerEvents) == 0 {
				t.Fatalf("actual new check/raw reviewer evidence lost: %+v", records)
			}
		})
	}
}

func reopenOutcomeStore(t *testing.T, s *store.Store, path string) *store.Store {
	t.Helper()
	s.Close()
	next, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	return next
}

func TestOutcomeReviewMatrixAndReopen(t *testing.T) {
	for _, tc := range []struct {
		name, review string
		reviewErr    error
		want         outcome.Verification
		state        string
	}{
		{"PASS", "PASS: actual check", nil, outcome.Passed, plan.TaskDone},
		{"FAIL", "FAIL actual failure", nil, outcome.Rejected, plan.TaskFailed},
		{"empty", "", nil, outcome.Unverified, plan.TaskDone},
		{"unknown", "likely okay", nil, outcome.Unverified, plan.TaskDone},
		{"PASSABLE", "PASSABLE", nil, outcome.Unverified, plan.TaskDone},
		{"FAILURE", "FAILURE", nil, outcome.Unverified, plan.TaskDone},
		{"partial reviewer", "partial reviewer receipt", errors.New("review transport unavailable"), outcome.Unverified, plan.TaskDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, aid, ids, path := setupOutcome(t, 1)
			m := &outcomeModel{reply: "completed artifact", review: tc.review, reviewErr: tc.reviewErr}
			d := depsFor(s, m)
			declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
			d.Wf.VerifyFollowup = true
			result, err := New(d).Execute(context.Background(), tool.Args{"approach": aid})
			if err != nil {
				t.Fatal(err)
			}
			qualified := tc.want == outcome.Passed
			if result.Workflow == nil || result.Workflow.Qualified != qualified || result.Failed == qualified {
				t.Fatalf("actual tool result=%+v", result)
			}
			s = reopenOutcomeStore(t, s, path)
			task, err := plan.GetTask(s, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if task.Execution != outcome.Completed || task.Verification != tc.want || task.State != tc.state || task.Qualified() != qualified || task.Result != "completed artifact" {
				t.Fatalf("owning reopened task=%+v", task)
			}
			if !strings.Contains(task.Review, tc.review) {
				t.Errorf("raw review lost: %q", task.Review)
			}
			var records []attemptRecord
			if err := json.Unmarshal([]byte(task.AttemptLog), &records); err != nil {
				t.Fatal(err)
			}
			if len(records) != 3 || records[0].Kind != "execution" || records[1].Kind != "check" || records[1].ExitCode == nil || *records[1].ExitCode != 0 || records[2].Kind != "review" || len(records[0].Transcript) < 3 {
				t.Fatalf("actual attempt evidence=%+v", records)
			}
			if tc.reviewErr != nil && (!strings.Contains(records[2].Error, tc.reviewErr.Error()) || !strings.Contains(records[2].Output, tc.review)) {
				t.Errorf("review partial/cause lost: %+v", records[1])
			}
			a, _ := plan.GetApproach(s, aid)
			if (a.State == plan.ApproachComplete) != qualified {
				t.Errorf("approach=%+v", a)
			}
		})
	}
}

func TestOutcomeRequiredReviewResumePolicy(t *testing.T) {
	s, aid, ids, path := setupOutcome(t, 1)
	m := &outcomeModel{reply: "original execution", review: "ambiguous"}
	d := depsFor(s, m)
	declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
	d.Wf.VerifyFollowup = true
	if _, err := Launch(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	s = reopenOutcomeStore(t, s, path)
	d.Store = s
	d.Wf.VerifyFollowup = false
	m.review = "PASS explicit follow-up"
	if err := Pause(s, aid); err != nil {
		t.Fatal(err)
	}
	results, err := Resume(context.Background(), d, aid)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := plan.GetTask(s, ids[0])
	if !task.Qualified() || task.Verification != outcome.Passed || m.workers != 1 || task.Attempts != 1 || !results[0].Reused {
		t.Fatalf("resume invented execution/policy: %+v workers=%d results=%+v", task, m.workers, results)
	}
	var records []attemptRecord
	_ = json.Unmarshal([]byte(task.AttemptLog), &records)
	if len(records) != 5 || records[0].Verification != outcome.Unverified {
		t.Errorf("admission/previous review overwritten: %+v", records)
	}
}

func TestOutcomeLegacyCompleteNeverQualified(t *testing.T) {
	s, aid, ids, path := setupOutcome(t, 1)
	// Actual historical database shape: lifecycle/output exist, all new fields NULL.
	if err := s.Write(`MATCH(x:Task) WHERE x.id=$id SET x.state='done',x.result='legacy output',x.review='PASS old prose',x.execution=NULL,x.execution_reason=NULL,x.verification=NULL,x.verification_reason=NULL,x.attempt_log=NULL`, map[string]any{"id": ids[0]}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='complete'`, map[string]any{"id": aid}); err != nil {
		t.Fatal(err)
	}
	s = reopenOutcomeStore(t, s, path)
	task, _ := plan.GetTask(s, ids[0])
	if task.Qualified() || task.Execution != outcome.Unknown || task.Verification != outcome.Unverified || task.Result != "legacy output" || task.Review != "PASS old prose" {
		t.Fatalf("legacy conversion=%+v", task)
	}
	qualified, err := plan.ApproachQualified(s, aid)
	if err != nil || qualified {
		t.Fatalf("legacy qualified=%v err=%v", qualified, err)
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRetired); err == nil {
		t.Fatal("legacy unverified retired as completed")
	}
	// A legacy paused lifecycle cannot gain execution proof from reviewer-only Resume/config-off.
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='paused'`, map[string]any{"id": aid}); err != nil {
		t.Fatal(err)
	}
	m := &outcomeModel{reply: "must not execute", review: "PASS must not review"}
	d := depsFor(s, m)
	d.Wf.VerifyFollowup = false
	results, err := Resume(context.Background(), d, aid)
	if err != nil {
		t.Fatal(err)
	}
	if m.calls != 0 || results[0].Qualified() {
		t.Fatalf("legacy inferred execution from review: calls=%d results=%+v", m.calls, results)
	}
	task, _ = plan.GetTask(s, ids[0])
	if task.Execution != outcome.Unknown || task.Verification != outcome.Unverified {
		t.Fatalf("legacy metadata relabeled: %+v", task)
	}
}

func TestOutcomeStoppedRequiresExplicitRequeue(t *testing.T) {
	s, aid, ids, path := setupOutcome(t, 1)
	m := &outcomeModel{reply: "```tool\ntool: absent\nx: y\n```"}
	d := depsFor(s, m)
	d.MaxSteps = 1
	d.Wf.MaxRetries = 2
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Outcome.Status != outcome.Stopped || results[0].Qualified() || m.workers != 1 {
		t.Fatalf("managed stop=%+v workers=%d", results, m.workers)
	}
	if err := Pause(s, aid); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	if m.workers != 1 {
		t.Fatal("resume auto-retried managed stop")
	}
	if _, err := plan.TransitionTask(s, ids[0], plan.TaskQueued); err != nil {
		t.Fatal(err)
	}
	if err := Pause(s, aid); err != nil {
		t.Fatal(err)
	}
	m.reply = "actual second execution"
	m.review = "PASS: explicit requeue component"
	if _, err := Resume(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	s = reopenOutcomeStore(t, s, path)
	task, _ := plan.GetTask(s, ids[0])
	if !task.Qualified() || task.Attempts != 2 || m.workers != 2 {
		t.Fatalf("explicit requeue=%+v workers=%d", task, m.workers)
	}
	var records []attemptRecord
	_ = json.Unmarshal([]byte(task.AttemptLog), &records)
	originalReply := false
	if len(records) > 0 {
		for _, message := range records[0].Transcript {
			originalReply = originalReply || message.Role == "assistant" && strings.Contains(message.Content, "tool: absent")
		}
	}
	if len(records) != 4 || records[2].Kind != "check" || records[3].Kind != "review" || records[0].Outcome.Status != outcome.Stopped || !originalReply {
		t.Fatalf("original stopped trace discarded: %+v", records)
	}
}

func TestOutcomeCancelledBeforeAdmissionAndRetryWait(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			s, aid, ids, path := setupOutcome(t, 1)
			m := &outcomeModel{reply: "should not run", failFirst: true, partial: "raw failed attempt"}
			d := depsFor(s, m)
			d.Wf.MaxRetries = 2
			d.Wf.RetryDelaySeconds = 2
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !retry {
				cancel()
			} else {
				go func() {
					deadline := time.Now().Add(time.Second)
					for time.Now().Before(deadline) {
						task, err := plan.GetTask(s, ids[0])
						if err == nil && task.State == plan.TaskQueued && task.Attempts == 1 {
							cancel()
							return
						}
						time.Sleep(time.Millisecond)
					}
					cancel()
				}()
			}
			results, err := Launch(ctx, d, aid)
			if err != nil {
				t.Fatal(err)
			}
			s = reopenOutcomeStore(t, s, path)
			task, _ := plan.GetTask(s, ids[0])
			want := 0
			if retry {
				want = 1
			}
			if task.State != plan.TaskCancelled || task.Execution != outcome.Cancelled || task.Attempts != want || m.workers != want || results[0].Qualified() {
				t.Fatalf("cancel metadata=%+v workers=%d results=%+v", task, m.workers, results)
			}
			report, err := WorkflowReport(Deps{Store: s}, aid, nil)
			if err != nil || report.Outcome.ExitCode() != 130 {
				t.Fatalf("cancel report=%+v err=%v", report, err)
			}
			if retry && !strings.Contains(task.AttemptLog, "raw failed attempt") {
				t.Fatal("retry cancellation lost prior raw attempt")
			}
		})
	}
}

func TestOutcomeActualWorkflowRefusalReachesParent(t *testing.T) {
	for _, surface := range []string{"launch", "workflow_resume", "workflow_status"} {
		t.Run(surface, func(t *testing.T) {
			s, aid, _, _ := setupOutcome(t, 1)
			d := depsFor(s, &outcomeModel{reply: "worker should not be admitted"})
			d.AuthMode = "authorize-all"
			d.Approve = func(context.Context, string) bool { return false }
			reg := tool.NewRegistry()
			reg.Register(New(d))
			for _, def := range NewBuild(d) {
				reg.Register(def)
			}
			parent := &agent.Runner{Model: &loopModel{replies: []string{"```tool\ntool: " + surface + "\napproach: " + aid + "\n```", "Everything succeeded."}}, Registry: reg, MaxSteps: 2, Exec: agent.Direct}
			raw, _, err := parent.Run(context.Background(), "component parent invocation")
			if err != nil || raw != "Everything succeeded." || parent.Outcome.ExitCode() != 1 {
				t.Fatalf("refusal masked: raw=%q outcome=%+v err=%v", raw, parent.Outcome, err)
			}
		})
	}
}

func setupOutcome(t *testing.T, n int) (*store.Store, string, []string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	s, aid, ids := setupAt(t, n, path)
	return s, aid, ids, path
}

func TestOutcomeRequiredBlankMalformedAndStoppedReviews(t *testing.T) {
	for _, name := range []string{"blank verify", "malformed review template", "review managed stop", "review cancellation"} {
		t.Run(name, func(t *testing.T) {
			s, aid, ids, path := setupOutcome(t, 1)
			m := &outcomeModel{reply: "worker artifact", review: "```tool\ntool: absent\nx: y\n```"}
			d := depsFor(s, m)
			declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
			d.Wf.VerifyFollowup = true
			d.MaxSteps = 1
			beforeTask, err := plan.GetTask(s, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			beforeApproach, err := plan.GetApproach(s, aid)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "blank verify":
				// Explicit legacy corruption fixture: owning edits now freeze Ready briefs.
				if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.verify=' '`, map[string]any{"id": ids[0]}); err != nil {
					t.Fatal(err)
				}
			case "malformed review template":
				d.TemplatesDir = t.TempDir()
				writeLegacyLaunchCatalog(t, d.TemplatesDir, map[string]string{"templates.subagent": "{{.Prompt}}", "templates.review": "{{if}}"})
			case "review cancellation":
				m.reviewErr = context.Canceled
				m.review = "raw cancelled review"
			}
			results, err := Launch(context.Background(), d, aid)
			if name == "malformed review template" {
				if err == nil || !strings.Contains(err.Error(), "templates.review") || len(results) != 0 || m.calls != 0 {
					t.Fatalf("invalid full catalog admitted model/effects: calls=%d results=%+v err=%v", m.calls, results, err)
				}
				s = reopenOutcomeStore(t, s, path)
				task, _ := plan.GetTask(s, ids[0])
				approach, _ := plan.GetApproach(s, aid)
				if !reflect.DeepEqual(task, beforeTask) || !reflect.DeepEqual(approach, beforeApproach) || task.State != plan.TaskQueued || task.Execution != outcome.Unknown || task.Qualified() {
					t.Fatalf("preflight failure changed truthful task/Ready state: task=%+v approach=%+v", task, approach)
				}
				report, err := WorkflowReport(Deps{Store: s}, aid, nil)
				if err != nil || report.Qualified || report.Outcome.ExitCode() != 1 {
					t.Fatalf("unexecuted work falsely qualified: report=%+v err=%v", report, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s = reopenOutcomeStore(t, s, path)
			task, _ := plan.GetTask(s, ids[0])
			if task.Execution != outcome.Completed || task.Result != "worker artifact" || task.Verification != outcome.Unverified || task.State != plan.TaskDone || task.Qualified() || task.VerificationReason == "" {
				t.Fatalf("required review falsely qualified/lost artifact: %+v", task)
			}
			report, err := WorkflowReport(Deps{Store: s}, aid, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if name == "review cancellation" {
				want = 130
			}
			if report.Qualified || report.Outcome.ExitCode() != want {
				t.Fatalf("review report=%+v results=%+v", report, results)
			}
			if name == "blank verify" && m.calls != 1 {
				t.Errorf("invented reviewer call %d", m.calls)
			}
		})
	}
}

type closingOutcomeModel struct{ s *store.Store }

func (m closingOutcomeModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	m.s.Close()
	return "actual collected artifact before persistence failure", "component", llm.Usage{}, nil
}
func TestOutcomePersistenceErrorKeepsCollectedResults(t *testing.T) {
	s, aid, _ := setup(t, 1)
	d := depsFor(s, closingOutcomeModel{s})
	results, err := Launch(context.Background(), d, aid)
	if err == nil || len(results) != 1 || results[0].Output != "actual collected artifact before persistence failure" || results[0].Qualified() {
		t.Fatalf("lost report or false success: results=%+v err=%v", results, err)
	}
}

func TestOutcomeExplicitLegacyReworkRetainsPriorArtifacts(t *testing.T) {
	s, aid, ids := setup(t, 1)
	if err := s.Write(`MATCH(x:Task) WHERE x.id=$id SET x.state='done',x.result='legacy raw artifact',x.review='legacy raw review',x.attempts=2,x.execution=NULL,x.verification=NULL,x.attempt_log=NULL`, map[string]any{"id": ids[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, ids[0], plan.TaskFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, ids[0], plan.TaskQueued); err != nil {
		t.Fatal(err)
	}
	m := &outcomeModel{reply: "new actual artifact", review: "PASS: explicit rework component"}
	if _, err := Launch(context.Background(), depsFor(s, m), aid); err != nil {
		t.Fatal(err)
	}
	task, _ := plan.GetTask(s, ids[0])
	var records []attemptRecord
	if err := json.Unmarshal([]byte(task.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[2].Kind != "check" || records[3].Kind != "review" || records[0].Kind != "prior_state" || records[0].Outcome.Status != outcome.Unknown || records[0].Output != "legacy raw artifact" || records[0].Review != "legacy raw review" || records[1].Attempt != 3 || task.Attempts != 3 || !task.Qualified() {
		t.Fatalf("prior facts invented/lost: task=%+v records=%+v", task, records)
	}
}

// A plain model double captures actual public Launch role requests. The host
// checker and owning settlement remain real; no semantic-review claim is made.
type handoffModel struct{ prompts []string }

func (m *handoffModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	prompt := fixtureRoleRequest(msgs)
	// Delivered advice may follow the actual owning role request. Identify
	// that request rather than treating the latest note as a worker prompt.
	for _, msg := range msgs {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "Review ") {
			prompt = msg.Content
			break
		}
	}
	m.prompts = append(m.prompts, prompt)
	if strings.HasPrefix(prompt, "Review ") {
		return "PASS: component verdict", "component", llm.Usage{}, nil
	}
	return "Worker claims a new implementation", "component", llm.Usage{}, nil
}
func TestLaunchReviewReceivesActualRoleRequestAndEvidence(t *testing.T) {
	s, aid, ids := setup(t, 1)
	declareFixtureCheck(t, s, aid, ids, "true")
	m := &handoffModel{}
	d := depsFor(s, m)
	d.Wf.VerifyFollowup = true
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.prompts) != 2 || !results[0].Qualified() {
		t.Fatalf("actual launch %v %+v", m.prompts, results)
	}
	worker, review := m.prompts[0], m.prompts[1]
	if !strings.Contains(review, "original execution requirements: "+worker) || !strings.Contains(review, "host worker execution evidence: Model-reported response. Bash receipts: 0 settled") {
		t.Fatalf("rendered child role/evidence missing: %s", review)
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"frozen task request: " + task.Prompt, "current: " + task.Current, "target: " + task.Target, "sketch: " + task.Sketch, "tests: " + task.Tests, "acceptance: " + task.Acceptance, "Verify instructions: " + task.Verify} {
		if !strings.Contains(review, want) {
			t.Errorf("missing frozen field %q", want)
		}
	}
	prefix := "current typed check facts: "
	at := strings.Index(review, prefix)
	if at < 0 {
		t.Fatalf("missing actual check: %s", review)
	}
	var coding session.CodingState
	if err = json.Unmarshal([]byte(strings.SplitN(review[at+len(prefix):], "\n", 2)[0]), &coding); err != nil || !coding.Passed() || coding.TaskID != task.ID || coding.Attempt != task.Attempts {
		t.Fatalf("invalid child current check %+v %v", coding, err)
	}
	if strings.Index(review, prefix) >= strings.Index(review, "worker report (unverified):") {
		t.Fatal("claims precede current facts")
	}
}
