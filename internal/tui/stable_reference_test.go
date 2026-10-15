package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func stablePlansFixture(t *testing.T, names ...string) (Model, []string, []string, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var ps, as, ts []string
	if len(names) == 0 {
		names = []string{"Alpha", "Beta"}
	}
	for _, name := range names {
		p, err := plan.CreatePlan(s, name, "goal")
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p.ID)
		for _, title := range []string{"first", "second"} {
			a, err := plan.CreateApproach(s, p.ID, name+title)
			if err != nil {
				t.Fatal(err)
			}
			as = append(as, a.ID)
			ts = append(ts, draftTask(t, s, a.ID, name+title))
			for _, to := range []string{plan.ApproachReady, plan.ApproachRunning, plan.ApproachComplete} {
				if _, err := fixtureApproachTransition(s, a.ID, to); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, to := range []string{plan.PlanActive, plan.PlanComplete} {
			if _, err := plan.TransitionPlan(s, p.ID, to); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.Close()
	m := New(testCfg(), "test")
	m.StorePath = path
	m.navigate(ViewPlans, "")
	m.onPlansLoaded(m.refreshPlans("")().(plansLoadedMsg))
	return m, ps, as, ts
}

func TestStablePlansHistoryAndCommentTargets(t *testing.T) {
	m, ps, as, ts := stablePlansFixture(t)
	m.onPlansLoaded(m.refreshPlans(ts[2])().(plansLoadedMsg))
	m = enter(m)
	if m.Plans.Detail == nil || m.Plans.Detail.Ref != "P2-A1-T1" {
		t.Errorf("qualified detail=%+v", m.Plans.Detail)
	}
	m = key(m, "f")
	if m.Input.Value() != "/comment P2-A1-T1 " {
		t.Errorf("prefill=%q", m.Input.Value())
	}
	m.Input.Reset()
	m.commentChat([]string{"A1-T1", "ambiguous"})
	if len(m.Comment) != 0 || !strings.Contains(lastAlert(m).Text, "P1-A1-T1") || !strings.Contains(lastAlert(m).Text, "P2-A1-T1") {
		t.Errorf("ambiguity queues/wrong alternatives: %+v %+v", m.Comment, m.Alerts)
	}
	m.commentChat([]string{ts[2], "[1]", "exact"})
	if len(m.Comment) != 1 || m.Comment[0].Ref != "P2-A1-T1" || !strings.Contains(m.Comment[0].Quote, "Betafirst") {
		t.Errorf("UUID quote target=%+v", m.Comment)
	}
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionApproach(s, as[0], plan.ApproachRetired); err != nil {
		t.Fatal(err)
	}
	s.Close()
	m.onPlansLoaded(m.refreshPlans(as[1])().(plansLoadedMsg))
	if r := m.plansCur(); r == nil || r.Ref != "P1-A2" {
		t.Errorf("retired sibling renumbered: %+v", r)
	}
	s, err = store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, ps[0], plan.PlanRetired); err != nil {
		t.Fatal(err)
	}
	s.Close()
	m.commentChat([]string{"[1]", "retired implicit"})
	if len(m.Comment) != 1 {
		t.Error("implicit line comment queued retired detail")
	}
	m.onPlansLoaded(m.refreshPlans(ts[2])().(plansLoadedMsg))
	if r := m.plansCur(); r == nil || r.Ref != "P2-A1-T1" {
		t.Errorf("retired parent renumbered: %+v", r)
	}
	m.commentChat([]string{"A1-T1", "still ambiguous"})
	if len(m.Comment) != 1 {
		t.Error("retired alias candidate silently retargeted")
	}
	m.onPlansLoaded(m.refreshPlans(ts[0])().(plansLoadedMsg))
	if m.plansCur() != nil || m.Plans.Detail != nil || !strings.Contains(m.Plans.Err, "retired") {
		t.Errorf("retired target fallback: sel=%+v detail=%+v err=%q", m.plansCur(), m.Plans.Detail, m.Plans.Err)
	}
}

func TestStablePlansDirectAndDeferredOwnership(t *testing.T) {
	m, _, _, ts := stablePlansFixture(t)
	m.onPlansLoaded(m.refreshPlans(ts[0])().(plansLoadedMsg))
	m = enter(m)
	older := m.refreshPlans(ts[0])()
	m.onPlansLoaded(m.refreshPlans(ts[2])().(plansLoadedMsg))
	m = enter(m)
	if m.Plans.Detail == nil || m.Plans.Detail.T == nil || m.Plans.Detail.T.ID != ts[2] {
		t.Error("explicit UUID retained old detail")
	}
	m.onPlansLoaded(older.(plansLoadedMsg))
	if m.plansCur().ID != ts[2] || m.Plans.Detail.T.ID != ts[2] {
		t.Error("older refresh overwrote current target")
	}
	backLate := m.refreshPlans(ts[0])()
	m.navigate(ViewFiles, "")
	m.goBack()
	m.onPlansLoaded(backLate.(plansLoadedMsg))
	if m.plansCur().ID != ts[2] {
		t.Error("back navigation revived obsolete target")
	}
	forwardLate := m.refreshPlans(ts[0])()
	m.goFwd()
	m.goBack()
	m.onPlansLoaded(forwardLate.(plansLoadedMsg))
	if m.plansCur().ID != ts[2] {
		t.Error("forward roundtrip revived obsolete target")
	}
	tabLate := m.refreshPlans(ts[0])()
	m.newTab()
	m.switchTab(0)
	m.onPlansLoaded(tabLate.(plansLoadedMsg))
	if m.plansCur().ID != ts[2] {
		t.Error("tab roundtrip revived obsolete target")
	}
	late := m.refreshPlans(ts[0])()
	m.navigate(ViewChat, "")
	m.onPlansLoaded(late.(plansLoadedMsg))
	if m.plansCur().ID != ts[2] {
		t.Error("response after leaving view retargeted selection")
	}
	m.navigate(ViewPlans, "")
	r := m.plansCur()
	m.Plans.Collapsed[m.Plans.Plans[r.PI].P.ID] = true
	m.onPlansLoaded(m.refreshPlans("")().(plansLoadedMsg))
	if m.plansCur() == nil || m.plansCur().ID != ts[2] || m.Plans.Detail.T.ID != ts[2] {
		t.Error("ordinary refresh lost UUID behind collapsed ancestors")
	}
	m.onPlansLoaded(m.refreshPlans("missing-node")().(plansLoadedMsg))
	if m.plansCur() != nil || m.Plans.Detail != nil || m.Plans.Err == "" {
		t.Error("unknown explicit target silently falls back")
	}
}

func TestStablePlansUniqueAliasAndFileComments(t *testing.T) {
	m, _, _, ts := stablePlansFixture(t, "Only")
	m.commentChat([]string{"A1-T1", "unique"})
	if len(m.Comment) != 1 || m.Comment[0].Ref != "P1-A1-T1" {
		t.Errorf("unique alias=%+v", m.Comment)
	}
	m.commentChat([]string{"Assets/a.go", "file note"})
	if len(m.Comment) != 2 || m.Comment[1].Ref != "Assets/a.go" {
		t.Error("file comment became a plan target")
	}
	m.plansSelect(ts[1])
	m.plansDetail(m.plansCur())
	m.commentChat([]string{"P1-A1-T1", "[1]", "wrong quote"})
	if len(m.Comment) != 2 {
		t.Error("different UUID quoted current detail")
	}
}
