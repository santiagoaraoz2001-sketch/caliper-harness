package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestPlansDetailShowsAndApprovesElaboration(t *testing.T) {
	m := plansModel(t)
	s, err := store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := plan.CreateElaboration(context.Background(), s, "approach", "a1", "model-session", "Deliver one", "source", "project check", "scope drift", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.SubmitElaboration(context.Background(), s, saved.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	loaded, err := loadPlans(m.StorePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Plans.Plans = loaded
	m.Plans.rebuild("")
	m.plansSelect("a1")
	m.plansDetail(m.plansCur())
	detail := strings.Join(detailLogical(m.Plans.Detail), "\n")
	for _, want := range []string{"elaboration v1", "pending", "purpose:", "Deliver one", "inputs:", "source", "completion evidence:", "project check", "risks:", "scope drift"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, detail)
		}
	}
	next, cmd, handled := m.plansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(Model)
	if !handled || cmd == nil {
		t.Fatalf("approve key handled=%v cmd=%v", handled, cmd != nil)
	}
	s, err = store.Open(m.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	latest, err := plan.LatestElaboration(context.Background(), s, "approach", "a1")
	if err != nil || latest.Status != plan.ElaborationApproved || latest.DecisionBy != "tui-user" {
		t.Fatalf("latest after TUI approval = %+v %v", latest, err)
	}
}
