package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"testing"
)

func periodicPlansTick(t *testing.T, m Model) Model {
	t.Helper()
	updated, cmd := m.Update(statusTickMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("status tick did not schedule commands")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 3 {
		t.Fatal("missing Plans refresh")
	}
	updated, _ = m.Update(batch[2]()) // Only the real Plans command; no timers/runtime probes.
	return updated.(Model)
}

func TestPlansPeriodicRefusalAndExplicitRecovery(t *testing.T) {
	for _, kind := range []string{"retired", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			m, ps, _, ts := stablePlansFixture(t)
			ref := "A1-T1"
			if kind == "retired" {
				s, err := store.Open(m.StorePath)
				if err != nil {
					t.Fatal(err)
				}
				_, err = plan.TransitionPlan(s, ps[0], plan.PlanRetired)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				ref = ts[0]
			}
			deliver := func(ref string) {
				cmd := m.submit("/plans " + ref)
				if cmd == nil {
					t.Fatal("missing public Plans command")
				}
				updated, _ := m.Update(cmd())
				m = updated.(Model)
			}
			deliver(ref)
			refused := m.Plans.Err
			if refused == "" || m.plansCur() != nil || m.Plans.Detail != nil {
				t.Fatal("initial target not refused")
			}
			m = periodicPlansTick(t, m)
			if m.Plans.Err != refused || m.plansCur() != nil || m.Plans.Detail != nil {
				t.Fatal("background refresh retargeted refused request")
			}
			m = enter(m)
			if m.Plans.Detail != nil {
				t.Fatal("Enter opened unrelated target")
			}
			deliver(ts[2])
			m = enter(m)
			m = periodicPlansTick(t, m)
			if m.Plans.Err != "" || m.plansCur() == nil || m.plansCur().ID != ts[2] || m.Plans.Detail == nil || m.Plans.Detail.ID != ts[2] {
				t.Fatal("explicit valid target did not recover same-ID detail")
			}
			deliver(ref)
			m = key(m, "down")
			selected := m.plansCur()
			if selected == nil {
				t.Fatal("arrow did not select a visible row")
			}
			id := selected.ID
			m = periodicPlansTick(t, m)
			m = enter(m)
			if m.Plans.Err != "" || m.Plans.Detail == nil || m.Plans.Detail.ID != id {
				t.Fatal("explicit arrow selection did not recover")
			}
		})
	}
}
