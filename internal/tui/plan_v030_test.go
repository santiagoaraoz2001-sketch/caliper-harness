package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/tui/slash"
)

func TestPlanSlashAndElaborateKey(t *testing.T) {
	r := slash.New()
	for _, name := range []string{"plan", "library"} {
		cmd, _, ok := r.Parse("/" + name)
		if !ok || cmd.View == "" {
			t.Fatalf("/%s not registered: %+v %v", name, cmd, ok)
		}
	}
	m := plansModel(t)
	// Select first task row.
	m.Plans.Sel = 2
	next, _, handled := m.plansKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = next.(Model)
	if !handled || m.Tabs[m.Active].at.view != ViewChat {
		t.Fatalf("elaborate did not navigate to chat: handled=%v view=%s", handled, m.Tabs[m.Active].at.view)
	}
	if got := m.Input.Value(); !strings.Contains(got, "Elaborate P1-A1-T1") || !strings.Contains(got, "graph and PAT state") || !strings.Contains(got, "Do not change the plan unless I ask") {
		t.Fatalf("elaborate draft = %q", got)
	}
}

func TestPlanApprovalAutoOpensPlans(t *testing.T) {
	m := New(testCfg(), "test")
	ch := make(chan string, 1)
	next, cmd := m.Update(showDialogMsg{seq: m.Chat.Seq, kind: "auth", prompt: "Admit?\nPlan summary:\nGoal", answer: ch})
	m = next.(Model)
	if m.Chat.Dialog == nil || m.Tabs[m.Active].at.view != ViewPlans || cmd == nil {
		t.Fatalf("plan approval did not auto-open plans: view=%s cmd=%v dialog=%v", m.Tabs[m.Active].at.view, cmd != nil, m.Chat.Dialog != nil)
	}
}
