package launch

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// gateModel blocks its first Chat call until released: the pause key
// lands while task one is in flight, deterministically.
type gateModel struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (m *gateModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	if strings.HasPrefix(msgs[len(msgs)-1].Content, "Review ") {
		return "UNVERIFIED: pause fixture observes scheduling only", "gate-review", llm.Usage{}, nil
	}
	if m.calls.Add(1) == 1 {
		close(m.started)
		<-m.release
		return "done-0", "gate", llm.Usage{}, nil
	}
	return "done-late", "gate", llm.Usage{}, nil
}

func TestPauseResumeCycle(t *testing.T) {
	s, aid, _ := setup(t, 2)
	d := depsFor(s, &echoModel{})
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if err := Pause(s, aid); err != nil {
		t.Fatalf("pause: %s", err)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachPaused {
		t.Fatalf("state=%s", a.State)
	}
	results, err := Resume(context.Background(), d, aid)
	if err != nil {
		t.Fatalf("resume: %s", err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d", len(results))
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachRunning {
		t.Fatalf("state=%s", a.State)
	}
}

func TestPauseResumeRefusals(t *testing.T) {
	s, aid, _ := setup(t, 1)
	if err := Pause(s, aid); err == nil || !strings.Contains(err.Error(), "pause needs running") {
		t.Errorf("pause on ready: %v", err)
	}
	if _, err := Resume(context.Background(), depsFor(s, &echoModel{}), aid); err == nil ||
		!strings.Contains(err.Error(), "resume needs paused") {
		t.Errorf("resume on ready: %v", err)
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachPaused); err != nil {
		t.Fatal(err)
	}
	if _, err := Launch(context.Background(), depsFor(s, &echoModel{}), aid); err == nil ||
		!strings.Contains(err.Error(), "launch needs ready") {
		t.Errorf("launch on paused: %v", err)
	}
}

func TestMidFlightPause(t *testing.T) {
	s, aid, ids := setup(t, 2)
	g := &gateModel{started: make(chan struct{}), release: make(chan struct{})}
	d := depsFor(s, g)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan []Result, 1)
	go func() {
		results, err := Launch(ctx, d, aid)
		if err != nil {
			t.Errorf("launch: %s", err)
		}
		done <- results
	}()
	select {
	case <-g.started:
	case <-time.After(10 * time.Second):
		t.Fatal("task one never started")
	}
	// Task one is inside the model call: pause now.
	running := false
	for i := 0; i < 100; i++ {
		x, err := plan.GetTask(s, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if x.State == plan.TaskRunning {
			running = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !running {
		t.Fatal("task one never reached running")
	}
	if err := Pause(s, aid); err != nil {
		t.Fatalf("pause: %s", err)
	}
	close(g.release)
	var results []Result
	select {
	case results = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("launch never returned after pause")
	}
	if len(results) != 2 || results[0].Outcome.Status != outcome.Completed || results[0].Qualified() || !results[1].Skipped {
		t.Fatalf("results=%+v", results)
	}
	if x, _ := plan.GetTask(s, ids[1]); x.State != plan.TaskQueued {
		t.Fatalf("task two state=%s", x.State)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachPaused {
		t.Fatalf("state=%s", a.State)
	}
	// Resume rechecks the unverified predecessor; later ordered work remains queued.
	resumed, err := Resume(context.Background(), depsFor(s, g), aid)
	if err != nil {
		t.Fatalf("resume: %s", err)
	}
	if !resumed[0].Skipped || !resumed[1].Skipped || resumed[1].State != plan.TaskQueued || resumed[1].Attempts != 0 || resumed[1].Qualified() || g.calls.Load() != 1 {
		t.Fatalf("resumed=%+v", resumed)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachRunning {
		t.Fatalf("state=%s", a.State)
	}
}

func TestVerifyPassConfirms(t *testing.T) {
	s, aid, ids := setup(t, 2)
	d := depsFor(s, &loopModel{replies: []string{"worker one", "PASS acceptance confirmed", "worker two", "PASS acceptance confirmed"}})
	declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
	d.Wf.VerifyFollowup = true
	d.Sub.MaxSubagents = 1
	if _, err := Launch(context.Background(), d, aid); err != nil {
		t.Fatalf("launch: %s", err)
	}
	for _, id := range ids {
		x, err := plan.GetTask(s, id)
		if err != nil {
			t.Fatal(err)
		}
		if x.State != plan.TaskDone || x.Review == "" {
			t.Errorf("task=%+v", x)
		}
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachComplete {
		t.Errorf("state=%s", a.State)
	}
}

func TestVerifyFailReopens(t *testing.T) {
	s, aid, ids := setup(t, 1)
	d := depsFor(s, &loopModel{replies: []string{"done-A", "FAIL acceptance names exit code 0, got none"}})
	declareFixtureCheck(t, s, aid, ids, "printf CHECK_RECEIPT")
	d.Wf.VerifyFollowup = true
	if _, err := Launch(context.Background(), d, aid); err != nil {
		t.Fatalf("launch: %s", err)
	}
	x, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if x.State != plan.TaskFailed {
		t.Errorf("state=%s", x.State)
	}
	if !strings.Contains(x.Review, "FAIL") {
		t.Errorf("review=%q", x.Review)
	}
	if a, _ := plan.GetApproach(s, aid); a.State == plan.ApproachComplete {
		t.Errorf("rejected work must not complete")
	}
}

func TestVerifyFollowupOffCannotBypassDeclaredEpisodeReview(t *testing.T) {
	s, aid, ids := setup(t, 1)
	m := &loopModel{replies: []string{"done-A", "FAIL: required independent review rejected the artifact"}}
	if _, err := Launch(context.Background(), depsFor(s, m), aid); err != nil {
		t.Fatalf("launch: %s", err)
	}
	if len(m.replies) != 0 {
		t.Fatalf("declared episode requires review even with optional follow-up off: %v", m.replies)
	}
	x, _ := plan.GetTask(s, ids[0])
	if x.State != plan.TaskFailed || !strings.HasPrefix(x.Review, "FAIL") || x.Qualified() {
		t.Errorf("task=%+v", x)
	}
}

func TestApproveLaunchGate(t *testing.T) {
	s, aid, _ := setup(t, 1)
	ld := depsFor(s, &echoModel{})
	ld.AuthMode = config.AuthRisky
	ld.Approve = func(ctx context.Context, prompt string) bool {
		return strings.Contains(prompt, aid) && strings.Contains(prompt, "1 tasks")
	}
	def := New(ld)
	res, err := def.Execute(context.Background(), tool.Args{"approach": aid})
	if err != nil {
		t.Fatalf("approved launch: %s", err)
	}
	if !strings.Contains(res.Text, "0/1 tasks qualified") {
		t.Errorf("text=%q", res.Text)
	}
}

func TestApproveLaunchRefused(t *testing.T) {
	s, aid, ids := setup(t, 1)
	ld := depsFor(s, &echoModel{})
	ld.AuthMode = config.AuthRisky
	ld.Approve = func(context.Context, string) bool { return false }
	def := New(ld)
	if _, err := def.Execute(context.Background(), tool.Args{"approach": aid}); err == nil ||
		!strings.Contains(err.Error(), "refused by the user") {
		t.Fatalf("err=%v", err)
	}
	// Refusal runs nothing: still ready, still queued.
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachReady {
		t.Errorf("state=%s", a.State)
	}
	if x, _ := plan.GetTask(s, ids[0]); x.State != plan.TaskQueued {
		t.Errorf("state=%s", x.State)
	}
}

func TestApproveLaunchYoloSkips(t *testing.T) {
	s, aid, _ := setup(t, 1)
	ld := depsFor(s, &echoModel{})
	ld.AuthMode = config.AuthYOLO
	def := New(ld)
	if _, err := def.Execute(context.Background(), tool.Args{"approach": aid}); err != nil {
		t.Fatalf("yolo: %s", err)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachRunning {
		t.Errorf("state=%s", a.State)
	}
}

func TestBuildToolsChain(t *testing.T) {
	// setup() readies its approach; the chain builds its own plan.
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	t.Cleanup(s.Close)
	d := depsFor(s, &echoModel{})
	defs := NewBuild(d)
	byName := map[string]func(context.Context, tool.Args) (tool.Result, error){}
	for _, def := range defs {
		byName[def.Name] = def.Execute
	}
	for _, name := range []string{"workflow_plan", "workflow_approach", "workflow_task", "workflow_block", "workflow_status", "workflow_pause", "workflow_resume"} {
		if byName[name] == nil {
			t.Fatalf("missing tool %s", name)
		}
	}
	r, err := byName["workflow_plan"](context.Background(), tool.Args{"title": "p2", "goal": "g2"})
	if err != nil {
		t.Fatal(err)
	}
	pid := lastWord(r.Text)
	r, err = byName["workflow_approach"](context.Background(), tool.Args{"plan": pid, "title": "a2"})
	if err != nil {
		t.Fatal(err)
	}
	aid := lastWord(r.Text)
	r, err = byName["workflow_task"](context.Background(), tool.Args{"approach": aid, "title": "t", "prompt": "do it"})
	if err != nil {
		t.Fatal(err)
	}
	tid := lastWord(r.Text)
	if _, err := byName["workflow_block"](context.Background(), tool.Args{"task": tid,
		"current": "n", "target": "t", "sketch": "s",
		"acceptance": "exit code 0", "tests": "T", "verify": "run it", "check_command": "printf FIXTURE_SUITE", "acceptance_command": "printf FIXTURE_ACCEPTANCE"}); err != nil {
		t.Fatal(err)
	}
	r, err = byName["workflow_status"](context.Background(), tool.Args{"approach": aid})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "is draft") {
		t.Errorf("status=%q", r.Text)
	}
	// Ready it through the real gate, then pause needs running.
	if _, err := fixtureApproachTransition(s, aid, plan.ApproachReady); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["workflow_pause"](context.Background(), tool.Args{"approach": aid}); err == nil {
		t.Fatal("pause on ready must refuse")
	}
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["workflow_pause"](context.Background(), tool.Args{"approach": aid}); err != nil {
		t.Fatal(err)
	}
	r, err = byName["workflow_status"](context.Background(), tool.Args{"approach": aid})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "is paused") {
		t.Errorf("status=%q", r.Text)
	}
	if _, err := byName["workflow_resume"](context.Background(), tool.Args{"approach": aid}); err != nil {
		t.Fatalf("resume: %s", err)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachRunning {
		t.Errorf("state=%s", a.State)
	}
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if f[0] == "created" {
		return f[2]
	}
	return f[1]
}

func TestIsFail(t *testing.T) {
	for in, want := range map[string]bool{
		"PASS looks right": false,
		"FAIL missing":     true,
		"fail lowercase":   true,
		"  fail padded":    true,
		"passed anyway":    false,
		"":                 false,
	} {
		if got := isFail(in); got != want {
			t.Errorf("isFail(%q)=%v", in, got)
		}
	}
}

func TestWorkflowRetireTool(t *testing.T) {
	s, aid, _ := setup(t, 1)
	// Explicit component review permits the lifecycle/retirement assertion only.
	d := depsFor(s, &outcomeModel{reply: "retirement fixture artifact", review: "PASS: lifecycle component"})
	if _, err := Launch(context.Background(), d, aid); err != nil {
		t.Fatalf("launch: %s", err)
	}
	defs := NewBuild(d)
	byName := map[string]func(context.Context, tool.Args) (tool.Result, error){}
	for _, def := range defs {
		byName[def.Name] = def.Execute
	}
	if byName["workflow_retire"] == nil {
		t.Fatal("missing workflow_retire")
	}
	// Ready work refuses: legal only from complete. Separate map:
	// each tool closes over its own store.
	s2, aid2, _ := setup(t, 1)
	byName2 := map[string]func(context.Context, tool.Args) (tool.Result, error){}
	for _, def := range NewBuild(depsFor(s2, &echoModel{})) {
		byName2[def.Name] = def.Execute
	}
	if _, err := byName2["workflow_retire"](context.Background(), tool.Args{"target": aid2}); err == nil {
		t.Fatal("retire on ready must refuse")
	}
	if _, err := byName2["workflow_retire"](context.Background(), tool.Args{"target": "nope"}); err == nil {
		t.Fatal("retire on missing must fail")
	}
	r, err := byName["workflow_retire"](context.Background(), tool.Args{"target": aid})
	if err != nil {
		t.Fatalf("retire: %s", err)
	}
	if !strings.Contains(r.Text, "retired approach") {
		t.Errorf("text=%q", r.Text)
	}
	if a, _ := plan.GetApproach(s, aid); a.State != plan.ApproachRetired {
		t.Errorf("state=%s", a.State)
	}
	// Plan-first: a completed plan retires as a plan.
	p, err := plan.CreatePlan(s, "pp", "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	child, err := plan.CreateApproach(s, p.ID, "qualified child")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, child.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(s, task.ID, "now", "after", "mech", "exit code 0", "TestX", "run it", "printf FIXTURE_SUITE", "printf FIXTURE_ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	if _, err = fixtureApproachTransition(s, child.ID, plan.ApproachReady); err != nil {
		t.Fatal(err)
	}
	if _, err = Launch(context.Background(), d, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionPlan(s, p.ID, plan.PlanComplete); err != nil {
		t.Fatal(err)
	}
	r, err = byName["workflow_retire"](context.Background(), tool.Args{"target": p.ID})
	if err != nil {
		t.Fatalf("plan retire: %s", err)
	}
	if !strings.Contains(r.Text, "retired plan") {
		t.Errorf("text=%q", r.Text)
	}
}
