package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func mkTask(t *testing.T, title, state string, ago time.Duration) *plan.Task {
	task := &plan.Task{ID: "task-" + title, Title: title, State: state, CreatedAt: time.Now().Add(-ago), Execution: outcome.Unknown, Verification: outcome.Unverified}
	if state == plan.TaskDone {
		pairedTUIFixture(t, task)
	}
	return task
}

func plansModel(t *testing.T) Model {
	t.Helper()
	m := New(testCfg(), "test")
	m.Width = 120
	m.Height = 40
	base := time.Now()
	p1 := LoadedPlan{P: &plan.Plan{ID: "p1", Title: "alpha", Goal: "g", State: plan.PlanActive, CreatedAt: base.Add(-time.Hour)}}
	a1 := LoadedApproach{A: &plan.Approach{ID: "a1", Title: "one", State: plan.ApproachRunning, CreatedAt: base}}
	// Created out of order: refs must follow creation time.
	a1.Tasks = []*plan.Task{mkTask(t, "two", plan.TaskDone, time.Minute), mkTask(t, "one", plan.TaskQueued, 2*time.Minute)}
	a2 := LoadedApproach{A: &plan.Approach{ID: "a2", Title: "two", State: plan.ApproachReady, CreatedAt: base.Add(time.Minute)}}
	p1.Approaches = []LoadedApproach{a1, a2}
	p2 := LoadedPlan{P: &plan.Plan{ID: "p2", Title: "beta", Goal: "g", State: plan.PlanDraft, CreatedAt: base}}
	m.Plans = PlansState{Plans: []LoadedPlan{p1, p2}, Loaded: true}
	m.StorePath = filepath.Join(t.TempDir(), "db")
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	write := func(q string, params map[string]any) {
		t.Helper()
		if err := s.Write(q, params); err != nil {
			t.Fatal(err)
		}
	}
	for _, lp := range m.Plans.Plans {
		p := lp.P
		write(`CREATE (:Plan {id:$id,title:$title,goal:$goal,state:$state,created_at:$at})`, map[string]any{"id": p.ID, "title": p.Title, "goal": p.Goal, "state": p.State, "at": p.CreatedAt})
		for _, la := range lp.Approaches {
			a := la.A
			write(`MATCH (p:Plan) WHERE p.id=$pid CREATE (a:Approach {id:$id,title:$title,state:$state,created_at:$at}) CREATE (p)-[:HAS_APPROACH]->(a)`, map[string]any{"pid": p.ID, "id": a.ID, "title": a.Title, "state": a.State, "at": a.CreatedAt})
			for _, x := range la.Tasks {
				write(`MATCH (a:Approach) WHERE a.id=$aid CREATE (x:Task {id:$id,title:$title,prompt:"",state:$state,created_at:$at,execution:$execution,verification:$verification,attempt_log:"[]"}) CREATE (a)-[:HAS_TASK]->(x)`, map[string]any{"aid": a.ID, "id": x.ID, "title": x.Title, "state": x.State, "at": x.CreatedAt, "execution": string(x.Execution), "verification": string(x.Verification)})
				if x.State == plan.TaskDone {
					recordTUICompleted(t, s, x.ID)
				}
			}
		}
	}
	s.Close()
	m.Plans.rebuild("")
	m.navigate(ViewPlans, "")
	return m
}

func TestPlansRefsOrder(t *testing.T) {
	m := plansModel(t)
	var refs []string
	for _, r := range m.Plans.Rows {
		refs = append(refs, r.Ref)
	}
	want := []string{"P1", "P1-A1", "P1-A1-T1", "P1-A1-T2", "P1-A2", "P2"}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Errorf("refs=%v titles=%v", refs, rowTitles(m))
	}
	if m.Plans.Rows[2].Label != "one" || m.Plans.Rows[3].Label != "two" {
		t.Errorf("tasks not in creation order: %v", rowTitles(m))
	}
}

func rowTitles(m Model) []string {
	var out []string
	for _, r := range m.Plans.Rows {
		out = append(out, r.Label)
	}
	return out
}

func TestPlansCounts(t *testing.T) {
	m := plansModel(t)
	if d, t2 := m.Plans.Rows[0].Done, m.Plans.Rows[0].Total; d != 1 || t2 != 2 {
		t.Errorf("plan count=%d/%d", d, t2)
	}
	if d, t2 := m.Plans.Rows[1].Done, m.Plans.Rows[1].Total; d != 1 || t2 != 2 {
		t.Errorf("approach count=%d/%d", d, t2)
	}
	if v := m.renderPlans(120, 30); !strings.Contains(v, "▓") || !strings.Contains(v, "1/2") {
		t.Errorf("render omits bar/counts:\n%s", v)
	}
}

func TestPlansNav(t *testing.T) {
	m := plansModel(t)
	m = key(m, "down")
	m = key(m, "down")
	if m.Plans.Sel != 2 {
		t.Fatalf("sel=%d", m.Plans.Sel)
	}
	m = key(m, "left")
	if len(m.Plans.Rows) != 4 {
		t.Fatalf("collapse must hide tasks: %d rows", len(m.Plans.Rows))
	}
	m = key(m, "right")
	if len(m.Plans.Rows) != 6 {
		t.Fatalf("expand must restore tasks: %d rows", len(m.Plans.Rows))
	}
	m = key(m, "up")
	m = key(m, "up")
	m = key(m, "up")
	if m.Plans.Sel != 0 {
		t.Errorf("clamp top: %d", m.Plans.Sel)
	}
}

func TestPlansDetail(t *testing.T) {
	m := plansModel(t)
	m.Plans.Plans[0].Approaches[0].Tasks[1].Result = "did it"
	m.Plans.Plans[0].Approaches[0].Tasks[1].Acceptance = "works"
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "down")
	m = enter(m)
	if m.Plans.Detail == nil || m.Plans.Detail.Ref != "P1-A1-T2" {
		t.Fatalf("detail=%+v", m.Plans.Detail)
	}
	if v := m.View(); !strings.Contains(v, "did it") || !strings.Contains(v, "accept:") {
		t.Errorf("detail omits fields:\n%s", v)
	}
	m = key(m, "esc")
	if m.Plans.Detail != nil {
		t.Fatal("esc must close detail")
	}
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Error("second esc must land in chat")
	}
}

func TestPlansTarget(t *testing.T) {
	m := plansModel(t)
	m.Plans.rebuild("A2")
	if got := m.Plans.Rows[m.Plans.Sel].Ref; got != "P1-A2" {
		t.Errorf("target sel=%q", got)
	}
	m.Plans.rebuild("bogus")
	if m.Plans.Sel != -1 || m.Plans.Err == "" {
		t.Errorf("bad target must refuse: sel=%d err=%q", m.Plans.Sel, m.Plans.Err)
	}
}

func TestPlansComment(t *testing.T) {
	m := plansModel(t)
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "f")
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Fatal("f must stay in the plan viewer")
	}
	if m.Plans.Detail == nil || m.Plans.Detail.Ref != "P1-A1-T1" {
		t.Fatal("f must open the dive for its row")
	}
	if got := m.Input.Value(); got != "/comment P1-A1-T1 " {
		t.Fatalf("prefill=%q", got)
	}
	m = submit(m, m.Input.Value()+"looks off")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "P1-A1-T1" || m.Comment[0].Text != "looks off" {
		t.Errorf("comment=%+v", m.Comment)
	}
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Error("sending comments must keep the viewer page")
	}
}

func TestPlansDiveAllLevels(t *testing.T) {
	m := plansModel(t)
	m = enter(m)
	if m.Plans.Detail == nil || m.Plans.Detail.Kind != rowPlan {
		t.Fatalf("plan dive=%+v", m.Plans.Detail)
	}
	if v := m.View(); !strings.Contains(v, "goal:") || !strings.Contains(v, "approaches:") {
		t.Errorf("plan dive omits content:\n%s", v)
	}
	m = key(m, "esc")
	m = key(m, "down")
	m = enter(m)
	if m.Plans.Detail == nil || m.Plans.Detail.Kind != rowApproach {
		t.Fatalf("approach dive=%+v", m.Plans.Detail)
	}
	if v := m.View(); !strings.Contains(v, "tasks:") || !strings.Contains(v, "A1-T1") {
		t.Errorf("approach dive omits tasks:\n%s", v)
	}
}

func TestCommentLineRefs(t *testing.T) {
	m := plansModel(t)
	m.Plans.Plans[0].Approaches[0].Tasks[0].Current = "do stuff"
	m.Plans.Plans[0].Approaches[0].Tasks[0].Acceptance = "works"
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "f")
	m = submit(m, "/comment A1-T1 [4-5] reword")
	if len(m.Comment) != 1 {
		t.Fatalf("comment=%+v Alerts=%+v", m.Comment, m.Alerts)
	}
	f := m.Comment[0]
	if f.Ref != "P1-A1-T1" || f.Lines != "[4-5]" || f.Text != "reword" {
		t.Errorf("parsed=%+v", f)
	}
	if f.Quote != "goal:\ndo stuff" {
		t.Errorf("quote=%q", f.Quote)
	}
	m = submit(m, "/comment A1-T1 [0] bad")
	if len(m.Comment) != 1 {
		t.Errorf("bad range must not queue: %+v", m.Comment)
	}
}

func TestParseLineSpec(t *testing.T) {
	got, err := parseLineSpec("[1-3, 5]")
	if err != nil || len(got) != 2 || got[0] != [2]int{1, 3} || got[1] != [2]int{5, 5} {
		t.Errorf("parsed=%v err=%v", got, err)
	}
	for _, bad := range []string{"", "x", "[]", "[0]", "[3-2]", "[a]", "[1,]", "[1-2"} {
		if isLinesSpec(bad) {
			t.Errorf("spec %q must not parse", bad)
		}
	}
}

func TestBuildPromptQuote(t *testing.T) {
	fb := []Comment{{Source: "plans", Ref: "A1", Lines: "[1-2]", Quote: "a\nb", Text: "fix"}}
	want := "User comments:\n- [A1] [1-2] fix\n> a\n> b\n\ngo"
	if got := buildPrompt("go", fb); got != want {
		t.Errorf("got %q", got)
	}
}

func TestPlansRefreshKey(t *testing.T) {
	m := plansModel(t)
	m.navigate(ViewPlans, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Error("r must return a refresh command")
	}
}

func TestPlansRender(t *testing.T) {
	m := plansModel(t)
	mv := m
	v := mv.renderPlans(120, 30)
	for _, want := range []string{"Plans", "P1", "A1-T1", "❯", "1/2 tasks", "enter dive", "r refresh"} {
		if !strings.Contains(v, want) {
			t.Errorf("render omits %q:\n%s", want, v)
		}
	}
	if n := len(strings.Split(v, "\n")); n != 30 {
		t.Errorf("render is %d rows, want 30", n)
	}
}

func TestPlansEmpty(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width = 120
	m.Height = 40
	m.Plans = PlansState{Sel: -1, Loaded: true}
	m.Plans.rebuild("")
	if v := m.renderPlans(120, 20); !strings.Contains(v, "No active plans") {
		t.Errorf("empty:\n%s", v)
	}
}

func TestPlansLoadError(t *testing.T) {
	m := plansModel(t)
	n := len(m.Plans.Rows)
	m.onPlansLoaded(plansLoadedMsg{err: errors.New("boom"), request: m.Plans.Request, owner: m.Chat.ID, path: m.StorePath})
	if len(m.Plans.Rows) != n || m.Plans.Err == "" {
		t.Errorf("error must keep stale rows + flag: rows=%d err=%q", len(m.Plans.Rows), m.Plans.Err)
	}
}

func TestPlansFailedState(t *testing.T) {
	m := plansModel(t)
	m.Plans.Plans[0].Approaches[0].Tasks[0].State = plan.TaskFailed
	m.Plans.rebuild("")
	v := m.renderPlans(120, 30)
	if !strings.Contains(v, "failed") {
		t.Errorf("failed state missing:\n%s", v)
	}
}

func draftTask(t *testing.T, s *store.Store, aid, title string) string {
	t.Helper()
	x, err := plan.CreateTask(s, aid, title, "do "+title)
	if err != nil {
		t.Fatal(err)
	}
	// The ready gate needs a verifiable block: command + count.
	if _, err := plan.SetTaskBlock(s, x.ID, "n", "t", "s", "reply has 5 lines", "T", "v", "printf UI_FIXTURE_SUITE", "printf UI_FIXTURE_ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{plan.TaskRunning, plan.TaskDone} {
		if _, err := plan.TransitionTask(s, x.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	recordTUICompleted(t, s, x.ID)
	return x.ID
}

func TestLoadPlansLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.CreatePlan(s, "live", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "fan")
	if err != nil {
		t.Fatal(err)
	}
	draftTask(t, s, a.ID, "second")
	draftTask(t, s, a.ID, "first")
	for _, to := range []string{plan.ApproachReady, plan.ApproachRunning, plan.ApproachComplete, plan.ApproachRetired} {
		if _, err := fixtureApproachTransition(s, a.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	a2, err := plan.CreateApproach(s, p.ID, "kept")
	if err != nil {
		t.Fatal(err)
	}
	draftTask(t, s, a2.ID, "solo")
	s.Close()
	lp, err := loadPlans(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lp) != 1 {
		t.Fatalf("plans=%+v", lp)
	}
	if len(lp[0].Approaches) != 2 || lp[0].Approaches[0].A.State != plan.ApproachRetired {
		t.Fatalf("retired approach must reserve history: %+v", lp[0].Approaches)
	}
	var st PlansState
	st.Plans = lp
	st.Loaded = true
	st.rebuild("")
	var refs []string
	for _, r := range st.Rows {
		refs = append(refs, r.Ref+" "+r.Label)
	}
	if got := strings.Join(refs, ","); got != "P1 live,P1-A2 kept,P1-A2-T1 solo" {
		t.Errorf("rows=%q", got)
	}
	if d, t2 := st.Rows[0].Done, st.Rows[0].Total; d != 1 || t2 != 1 {
		t.Errorf("count=%d/%d", d, t2)
	}
}
