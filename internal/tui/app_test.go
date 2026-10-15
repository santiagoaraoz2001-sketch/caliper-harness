package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/config"
)

func testCfg() config.Config {
	c := config.Defaults()
	c.Context.Window = 1000
	c.Watch.Enabled = false
	return c
}

func enter(m Model) Model {
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return um.(Model)
}

func key(m Model, s string) Model {
	var msg tea.KeyMsg
	switch s {
	case "ctrl+n":
		msg = tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+p":
		msg = tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+t":
		msg = tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+w":
		msg = tea.KeyMsg{Type: tea.KeyCtrlW}
	case "ctrl+o":
		msg = tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+g":
		msg = tea.KeyMsg{Type: tea.KeyCtrlG}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "shift+up":
		msg = tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		msg = tea.KeyMsg{Type: tea.KeyShiftDown}
	case "shift+left":
		msg = tea.KeyMsg{Type: tea.KeyShiftLeft}
	case "shift+right":
		msg = tea.KeyMsg{Type: tea.KeyShiftRight}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	um, _ := m.Update(msg)
	return um.(Model)
}

func submit(m Model, line string) Model {
	m.Input.SetValue(line)
	return enter(m)
}

func TestSubmitViewSlashNavigates(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/plans")
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Errorf("view=%q", m.Tabs[m.Active].at.view)
	}
	if len(m.Tabs[m.Active].back) != 1 {
		t.Errorf("back=%v", m.Tabs[m.Active].back)
	}
	m = submit(m, "/sessions abc123")
	if m.Tabs[m.Active].at.view != ViewSessions || m.Tabs[m.Active].at.target != "abc123" {
		t.Errorf("at=%+v", m.Tabs[m.Active].at)
	}
}

func TestSubmitUnknownSlashAlerts(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/bogus")
	if len(m.Alerts) != 1 || m.Alerts[0].Kind != "err" || !strings.Contains(m.Alerts[0].Text, "unknown") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestSubmitModelsOpensLiveStatus(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/models")
	if !m.serveActive() || len(m.Alerts) != 0 {
		t.Fatalf("view=%v alerts=%v", m.Tabs[m.Active].at.view, m.Alerts)
	}
}

func TestSubmitPlainTextAlerts(t *testing.T) {
	m := submit(New(testCfg(), "test"), "hello")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "no model") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestHelpOpensAndCloses(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/help")
	if !m.HelpOpen {
		t.Fatal("help must open")
	}
	if !strings.Contains(m.View(), "/diff") {
		t.Error("help view must list commands")
	}
	m = key(m, "x")
	if m.HelpOpen {
		t.Error("any key must close help")
	}
}

func TestTabsCycleAndManage(t *testing.T) {
	m := New(testCfg(), "test")
	m = key(m, "ctrl+t")
	if len(m.Tabs) != 2 || m.Active != 1 {
		t.Fatalf("tabs=%d active=%d", len(m.Tabs), m.Active)
	}
	m = key(m, "ctrl+p")
	if m.Active != 0 {
		t.Errorf("active=%d", m.Active)
	}
	m = key(m, "ctrl+n")
	if m.Active != 1 {
		t.Errorf("active=%d", m.Active)
	}
	m = key(m, "ctrl+w")
	if len(m.Tabs) != 1 || m.Active != 0 {
		t.Errorf("tabs=%d active=%d", len(m.Tabs), m.Active)
	}
	m = key(m, "ctrl+w")
	if len(m.Tabs) != 1 {
		t.Error("last tab must not close")
	}
}

func TestBackForward(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/plans")
	m = submit(m, "/status")
	m = key(m, "ctrl+o")
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Errorf("back lands on %q", m.Tabs[m.Active].at.view)
	}
	m = key(m, "ctrl+g")
	if m.Tabs[m.Active].at.view != ViewStatus {
		t.Errorf("forward lands on %q", m.Tabs[m.Active].at.view)
	}
	m = key(m, "ctrl+o")
	m = key(m, "ctrl+o")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Errorf("double back lands on %q", m.Tabs[m.Active].at.view)
	}
}

func TestSuggestionsAndTabAccept(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/p")
	if got := m.suggestions(); len(got) != 1 || got[0] != "/plans" {
		t.Errorf("suggestions=%v", got)
	}
	m = key(m, "tab")
	if m.Input.Value() != "/plans " {
		t.Errorf("accepted=%q", m.Input.Value())
	}
	m2 := New(testCfg(), "test")
	m2.Input.SetValue("hello")
	if got := m2.suggestions(); len(got) != 0 {
		t.Errorf("plain text suggests %v", got)
	}
}

func TestEnterAcceptsPartial(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/p")
	m = enter(m)
	if m.Input.Value() != "/plans " {
		t.Errorf("input=%q", m.Input.Value())
	}
	m = key(m, "x")
	if m.Input.Value() != "/plans x" {
		t.Errorf("type-after-accept=%q", m.Input.Value())
	}
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Error("must not navigate on accept")
	}
}

func TestEnterSendsExact(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/plans")
	m = enter(m)
	if m.Tabs[m.Active].at.view != ViewPlans {
		t.Errorf("view=%q", m.Tabs[m.Active].at.view)
	}
}

func TestEscDismissesAlert(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/bogus")
	m = submit(m, "/nope")
	m = key(m, "esc")
	if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "bogus") {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestAuthMenu(t *testing.T) {
	m := New(testCfg(), "test")
	m = submit(m, "/auth")
	if m.Input.Value() != "/auth " {
		t.Fatalf("input=%q", m.Input.Value())
	}
	if s := m.suggestions(); len(s) != 3 {
		t.Fatalf("menu=%v", s)
	}
	m.Input.SetValue("/auth r")
	m = enter(m)
	if m.Input.Value() != "/auth risky" {
		t.Fatalf("input=%q", m.Input.Value())
	}
	m = enter(m)
	if m.St.Auth != "RISKY" {
		t.Errorf("auth=%q alerts=%+v", m.St.Auth, m.Alerts)
	}
}

func TestAuthBogus(t *testing.T) {
	m := New(testCfg(), "test")
	before := m.Cfg.Auth.Mode
	m = submit(m, "/auth bogus")
	if m.Cfg.Auth.Mode != before {
		t.Error("mode must not change")
	}
	if last := m.Alerts[len(m.Alerts)-1]; last.Kind != "err" {
		t.Errorf("alerts=%+v", m.Alerts)
	}
}

func TestPickerPages(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/")
	if v := m.View(); !strings.Contains(v, "1/30") || !strings.Contains(v, "/chat") || strings.Contains(v, "/quit") {
		t.Fatal("page 1 must show head only")
	}
	m.PickerIdx = 20
	if v := m.View(); !strings.Contains(v, "21/30") || !strings.Contains(v, "…16 above") || strings.Contains(v, "/chat") {
		t.Error("page 3 must show tail with above-count")
	}
}

func TestQuitCommand(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/quit")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("/quit must return a command")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("ctrl+c must return a command")
	}
}

func TestViewRendersShell(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width = 120
	v := m.View()
	for _, want := range []string{"Chat", "❯", "─", "┌", "ctrl+t new tab", "last measured prompt "} {
		if !strings.Contains(v, want) {
			t.Errorf("view omits %q", want)
		}
	}
	m = submit(New(testCfg(), "test"), "/bogus")
	if !strings.Contains(m.View(), "unknown") {
		t.Error("alert line must show the error")
	}
}

func TestInputTitleHints(t *testing.T) {
	v := New(testCfg(), "test").View()
	for _, want := range []string{"draft 1/3", "⇧←→ drafts", "⇧↑↓ history"} {
		if !strings.Contains(v, want) {
			t.Errorf("input title omits %q", want)
		}
	}
	if strings.Index(v, "⇧←→") < strings.Index(v, "draft 1/3") {
		t.Error("draft/history hints must sit right of the draft marker")
	}
}

func TestTabHints(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width = 120
	first := strings.Join(m.tabChrome(), "\n")
	for _, want := range []string{"ctrl+t new tab", "ctrl+n/p switch tab", "ctrl+w close tab", "ctrl+o/g tab back/fwd"} {
		if !strings.Contains(first, want) {
			t.Errorf("tab row omits %q: %q", want, first)
		}
	}
}

func TestHelpShowsKeys(t *testing.T) {
	m := New(testCfg(), "test")
	m = submit(m, "/help")
	v := ""
	for i := 0; i < 20; i++ {
		v += m.View() + "\n"
		m = viewportKey(m, tea.KeyPgDown)
	}
	v = strings.Join(strings.Fields(v), " ")
	for _, want := range []string{"Commands & keys", "KEYS", "shift+←→ drafts", "tab comment", "pgup/pgdn scroll", "enter dive", "/auth"} {
		if !strings.Contains(v, want) {
			t.Errorf("help omits %q:\n%s", want, v)
		}
	}
}

func TestNewThreadsConfig(t *testing.T) {
	c := testCfg()
	c.Auth.Mode = "yolo"
	c.Params.Thinking = false
	c.Model.Name = "m"
	m := New(c, "test")
	if m.St.Auth != "YOLO" || m.St.Think || m.St.Model != "m" || m.St.CtxWindow != 1000 {
		t.Errorf("status=%+v", m.St)
	}
	if m.StorePath != c.Store.Path {
		t.Errorf("store=%q", m.StorePath)
	}
}

func TestStatusLiveMsgApplies(t *testing.T) {
	m := New(testCfg(), "test")
	um, _ := m.Update(statusLiveMsg{branch: "main", dir: "h", haveGit: true, planTitle: "p", planApproach: []string{"a 1/1"}, havePlan: true})
	m = um.(Model)
	if m.St.Branch != "main" || m.St.Dir != "h" || m.St.PlanTitle != "p" || len(m.St.PlanApproaches) != 1 {
		t.Errorf("status=%+v", m.St)
	}
	um, _ = m.Update(statusLiveMsg{})
	m = um.(Model)
	if m.St.Branch != "main" || m.St.PlanTitle != "p" {
		t.Errorf("stale segments must stay: %+v", m.St)
	}
}

func TestPickerTreeRows(t *testing.T) {
	m := New(testCfg(), "test")
	m.Input.SetValue("/p")
	v := m.View()
	for _, want := range []string{"↑↓ select", "└─", "/plans", "plan/approach/task trees"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker omits %q", want)
		}
	}
}
