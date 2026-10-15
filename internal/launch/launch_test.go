package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// echoModel answers with the task title line, tracking order and overlap.
type echoModel struct {
	mu         sync.Mutex
	order      []string
	cur, max   atomic.Int32
	sleep      time.Duration
	failSubstr string
	seen       []string
}

func (m *echoModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	if strings.HasPrefix(msgs[len(msgs)-1].Content, "Review ") {
		return "UNVERIFIED: this fixture observes worker scheduling only", "echo-review", llm.Usage{}, nil
	}
	cur := m.cur.Add(1)
	for {
		max := m.max.Load()
		if cur <= max || m.max.CompareAndSwap(max, cur) {
			break
		}
	}
	defer m.cur.Add(-1)
	if m.sleep > 0 {
		time.Sleep(m.sleep)
	}
	prompt := fixtureRoleRequest(msgs)
	line := strings.SplitN(prompt, "\n", 2)[0]
	m.mu.Lock()
	m.order = append(m.order, line)
	m.seen = append(m.seen, prompt)
	m.mu.Unlock()
	if m.failSubstr != "" && strings.Contains(prompt, m.failSubstr) {
		return "", "", llm.Usage{}, fmt.Errorf("model blew up on purpose")
	}
	return "result:" + line, "echo", llm.Usage{}, nil
}

// Real claimed workers receive their task at system role; reviewer requests
// remain user-role evidence. Fixture models identify the owning role, not the
// latest delivered comment or the executor's short start message.
func fixtureRoleRequest(messages []llm.Message) string {
	for _, m := range messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "Review ") {
			return m.Content
		}
	}
	for _, m := range messages {
		if m.Role == "system" && (strings.HasPrefix(m.Content, "Task ") || strings.HasPrefix(m.Content, "TASK ") || strings.Contains(m.Content, "\nTask ") || strings.Contains(m.Content, "\nTASK ")) {
			return m.Content
		}
	}
	return messages[len(messages)-1].Content
}

// flakyModel fails its first n calls, then answers.
type flakyModel struct {
	left atomic.Int32
}

func (m *flakyModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	if strings.HasPrefix(msgs[len(msgs)-1].Content, "Review ") {
		return "UNVERIFIED: retry fixture has no semantic reviewer", "flaky-review", llm.Usage{}, nil
	}
	if m.left.Add(-1) >= 0 {
		return "", "", llm.Usage{}, fmt.Errorf("transient bang")
	}
	return "recovered", "flaky", llm.Usage{}, nil
}

// setup builds a ready approach with n drafted tasks, returning store,
// approach id, and task ids.
func setup(t *testing.T, n int) (*store.Store, string, []string) {
	t.Helper()
	return setupAt(t, n, filepath.Join(t.TempDir(), "db"))
}
func setupAt(t *testing.T, n int, path string) (*store.Store, string, []string) {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	t.Cleanup(s.Close)
	p, err := plan.CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatalf("plan: %s", err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatalf("approach: %s", err)
	}
	var ids []string
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("t%d", i)
		x, err := plan.CreateTask(s, a.ID, title, "prompt-"+title)
		if err != nil {
			t.Fatalf("task: %s", err)
		}
		// Mechanical lifecycle commands, not evidence of product correctness.
		_, err = plan.SetTaskBlock(s, x.ID, "now", "after", "mech", "exit code is 0", "TestX", "run it", "printf FIXTURE_SUITE", "printf FIXTURE_ACCEPTANCE")
		if err != nil {
			t.Fatalf("block: %s", err)
		}
		ids = append(ids, x.ID)
	}
	if _, err := fixtureApproachTransition(s, a.ID, plan.ApproachReady); err != nil {
		t.Fatalf("ready: %s", err)
	}
	return s, a.ID, ids
}

// bindFixtureProject uses the actual owner before configured-workspace role execution.
func bindFixtureProject(t *testing.T, s *store.Store, aid, root string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.BindProject(t.Context(), s, p.ID, physical); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	return physical
}

func depsFor(s *store.Store, m agent.Model) Deps {
	return Deps{
		Store:    s,
		NewModel: func() agent.Model { return m },
		NewRegistry: func() *tool.Registry {
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.Defaults().Tools.Bash))
			return reg
		},
		MaxSteps:     5,
		TemplatesDir: "../../templates",
		ParentWindow: 0,
		Sub:          config.SubagentsConfig{Mode: config.SubagentsSequential, MaxResultChars: 2000},
		Wf:           config.WorkflowsConfig{MaxRetries: 0},
		Judge: func(_ context.Context, q decision.Request) (decision.Verdict, error) {
			return decision.Verdict{Option: q.Options[0], Confidence: 1}, nil
		},
		Threshold: 0.7,
	}
}

func TestLaunchSequential(t *testing.T) {
	s, aid, ids := setup(t, 3)
	m := &echoModel{}
	results, err := Launch(context.Background(), depsFor(s, m), aid)
	if err != nil {
		t.Fatalf("launch: %s", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r.TaskID != ids[i] {
			t.Fatalf("result %d task=%s, want %s", i, r.TaskID, ids[i])
		}
		if i == 0 {
			if r.Outcome.Status != outcome.Completed || r.Verification != outcome.Unverified || r.Err == nil || !strings.Contains(r.Output, "t0") {
				t.Fatalf("first result must retain actual completed but unqualified work: %+v", r)
			}
		} else if !r.Skipped || r.Reused || r.State != plan.TaskQueued || r.Attempts != 0 || r.Output != "" {
			t.Fatalf("ordered result %d ran without predecessor qualification: %+v", i, r)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.order) != 1 || !strings.Contains(m.order[0], "t0") {
		t.Fatalf("order=%v", m.order)
	}
	if !strings.Contains(m.seen[0], "acceptance: exit code is 0") {
		t.Fatalf("block missing from prompt:\n%s", m.seen[0])
	}
	for i, id := range ids {
		x, err := plan.GetTask(s, id)
		if err != nil {
			t.Fatalf("get: %s", err)
		}
		if i == 0 {
			if x.State != plan.TaskDone || x.Result == "" || x.Qualified() {
				t.Fatalf("task %+v", x)
			}
		} else if x.State != plan.TaskQueued || x.Attempts != 0 || x.Result != "" {
			t.Fatalf("ordered task %+v", x)
		}
	}
	a, err := plan.GetApproach(s, aid)
	if err != nil {
		t.Fatalf("get: %s", err)
	}
	if a.State != plan.ApproachRunning {
		t.Fatalf("approach state=%s", a.State)
	}
}

func TestLaunchConcurrentCapOne(t *testing.T) {
	s, aid, _ := setup(t, 3)
	m := &echoModel{sleep: 50 * time.Millisecond}
	d := depsFor(s, m)
	d.Sub.Mode, d.Sub.MaxSubagents = config.SubagentsConcurrent, 1
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("launch: %s", err)
	}
	for i, r := range results {
		if r.Outcome.Status != outcome.Completed || r.Verification != outcome.Unverified || r.Err == nil {
			t.Fatalf("result %d must retain completed work without reviewer qualification: %+v", i, r)
		}
	}
	if got := m.max.Load(); got != 1 {
		t.Fatalf("max overlap=%d with cap 1, want 1", got)
	}
}

func TestLaunchConcurrentOverlaps(t *testing.T) {
	s, aid, _ := setup(t, 3)
	m := &echoModel{sleep: 300 * time.Millisecond}
	d := depsFor(s, m)
	d.Sub.Mode, d.Sub.MaxSubagents = config.SubagentsConcurrent, 3
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("launch: %s", err)
	}
	for i, r := range results {
		if r.Outcome.Status != outcome.Completed || r.Verification != outcome.Unverified || r.Err == nil {
			t.Fatalf("result %d must retain completed work without reviewer qualification: %+v", i, r)
		}
		if r.Output == "" {
			t.Fatalf("result %d empty", i)
		}
	}
	if got := m.max.Load(); got < 2 {
		t.Fatalf("max overlap=%d, want >= 2 (no concurrency observed)", got)
	}
}

func TestLaunchFailureIsolates(t *testing.T) {
	s, aid, ids := setup(t, 3)
	m := &echoModel{failSubstr: "t1"}
	d := depsFor(s, m)
	d.Sub.Mode = config.SubagentsConcurrent // Independent siblings exercise isolation, not ordered prerequisites.
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("launch itself must not fail: %s", err)
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "blew up") {
		t.Fatalf("middle result=%+v, want failure", results[1])
	}
	if results[0].Outcome.Status != outcome.Completed || results[2].Outcome.Status != outcome.Completed || results[0].Qualified() || results[2].Qualified() {
		t.Fatalf("siblings must complete: %+v", results)
	}
	x, _ := plan.GetTask(s, ids[1])
	if x.State != plan.TaskFailed {
		t.Fatalf("failed task state=%s", x.State)
	}
	if !strings.Contains(x.ExecutionReason, "blew up") || x.Result != "" {
		t.Fatalf("failed result=%q", x.Result)
	}
	if results[1].Attempts != 1 || x.Attempts != 1 {
		t.Fatalf("no-retry attempts result=%d node=%d", results[1].Attempts, x.Attempts)
	}
	a, _ := plan.GetApproach(s, aid)
	if a.State != plan.ApproachRunning {
		t.Fatalf("approach state=%s, want running (not complete)", a.State)
	}
}

func TestLaunchGate(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	t.Cleanup(s.Close)
	p, _ := plan.CreatePlan(s, "p", "g")
	a, _ := plan.CreateApproach(s, p.ID, "a")
	d := depsFor(s, &echoModel{})
	if _, err := Launch(context.Background(), d, a.ID); err == nil {
		t.Fatal("launch on draft accepted, want rejection")
	} else if !strings.Contains(err.Error(), "is draft — launch needs ready") {
		t.Fatalf("wrong error: %s", err)
	}
	if _, err := Launch(context.Background(), d, "missing"); err == nil {
		t.Fatal("launch on missing accepted, want rejection")
	}
}

func TestLaunchToolTruncatesFeed(t *testing.T) {
	s, aid, _ := setup(t, 1)
	m := &echoModel{}
	d := depsFor(s, m)
	d.Sub.MaxResultChars = 10
	def := New(d)
	res, err := def.Execute(context.Background(), tool.Args{"approach": aid})
	if err != nil {
		t.Fatalf("execute: %s", err)
	}
	if !strings.Contains(res.Text, "0/1 tasks qualified") {
		t.Fatalf("missing count: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[truncated, full text on the task node]") {
		t.Fatalf("missing truncation marker: %q", res.Text)
	}
	tasks, _ := plan.ApproachTasks(s, aid)
	if len(tasks[0].Result) <= 10 {
		t.Fatalf("node result truncated too: %q", tasks[0].Result)
	}
}

func TestLaunchUnknownMode(t *testing.T) {
	s, aid, _ := setup(t, 1)
	d := depsFor(s, &echoModel{})
	d.Sub.Mode = "sideways"
	if _, err := Launch(context.Background(), d, aid); err == nil {
		t.Fatal("unknown mode accepted, want rejection")
	}
}

func TestLaunchRetrySuccess(t *testing.T) {
	s, aid, ids := setup(t, 1)
	m := &flakyModel{}
	m.left.Store(2)
	d := depsFor(s, m)
	d.Wf.MaxRetries = 2
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("launch: %s", err)
	}
	r := results[0]
	if r.Outcome.Status != outcome.Completed || r.Verification != outcome.Unverified || r.Err == nil {
		t.Fatalf("recovered worker needs independent review: %+v", r)
	}
	if r.Attempts != 3 || r.Output != "recovered" {
		t.Fatalf("result=%+v, want 3 attempts with recovery", r)
	}
	x, _ := plan.GetTask(s, ids[0])
	if x.State != plan.TaskDone || x.Attempts != 3 {
		t.Fatalf("node state=%s attempts=%d", x.State, x.Attempts)
	}
	a, _ := plan.GetApproach(s, aid)
	if a.State != plan.ApproachRunning {
		t.Fatalf("approach state=%s, want running without qualification", a.State)
	}
}

func TestLaunchRetryExhaustion(t *testing.T) {
	s, aid, ids := setup(t, 3)
	m := &echoModel{failSubstr: "t1"}
	d := depsFor(s, m)
	d.Sub.Mode = config.SubagentsConcurrent // Independent siblings permit the retry/isolation fixture.
	d.Wf.MaxRetries = 2
	results, err := Launch(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("launch itself must not fail: %s", err)
	}
	r := results[1]
	if r.Err == nil || r.Attempts != 3 {
		t.Fatalf("exhausted result=%+v, want error after 3 attempts", r)
	}
	var records []attemptRecord
	if err := json.Unmarshal([]byte(r.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("attempt records=%+v", records)
	}
	for i, record := range records {
		if record.Attempt != i+1 || !strings.Contains(record.Error, "blew up") {
			t.Errorf("record=%+v", record)
		}
	}

	if results[0].Outcome.Status != outcome.Completed || results[2].Outcome.Status != outcome.Completed || results[0].Qualified() || results[2].Qualified() {
		t.Fatalf("siblings must complete: %+v", results)
	}
	x, _ := plan.GetTask(s, ids[1])
	if x.State != plan.TaskFailed || x.Attempts != 3 {
		t.Fatalf("node state=%s attempts=%d", x.State, x.Attempts)
	}
	if !strings.Contains(x.AttemptLog, "model blew up") {
		t.Fatalf("node misses attempt log: %q", x.AttemptLog)
	}
	a, _ := plan.GetApproach(s, aid)
	if a.State != plan.ApproachRunning {
		t.Fatalf("approach state=%s, want running", a.State)
	}
}

func TestRunOneRetryDelay(t *testing.T) {
	s, _, ids := setup(t, 1)
	m := &flakyModel{}
	m.left.Store(1)
	d := depsFor(s, m)
	x, _ := plan.GetTask(s, ids[0])
	r := runOne(context.Background(), d, x, 0, 1, time.Nanosecond)
	if r.Err != nil || r.Attempts != 2 {
		t.Fatalf("result=%+v, want success on attempt 2", r)
	}
}
