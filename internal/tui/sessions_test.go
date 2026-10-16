package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/session"
)

// sessionsModel builds a model on a temp sessions dir with two runs
// (bb newer) plus one corrupt file, already navigated to the view.
func sessionsModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	mk := func(id, prompt string, tm time.Time, n int) {
		if err := session.Append(dir, id, session.Event{Time: tm, Kind: "prompt", Text: prompt}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if err := session.Append(dir, id, session.Event{Kind: "reply", Text: "r"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := session.Append(dir, id, session.Event{Kind: "done", Text: "final"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("20260101-000000-aa", "first run", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1)
	mk("20260202-000000-bb", "second run\nmultiline", time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), 2)
	if err := os.WriteFile(filepath.Join(dir, "zzz.jsonl"), []byte("not json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := submit(New(testCfg(), "test"), "/sessions")
	m.SessionsPath = dir
	m.CfgPath = filepath.Join(t.TempDir(), "specific-harness.yaml")
	return m
}

func loadSessions(m Model) Model {
	um, _ := m.Update(m.refreshSessions()())
	return um.(Model)
}

func TestSessionsListNewestFirst(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	if len(m.Sessions.Rows) != 2 {
		t.Fatalf("rows=%+v skipped=%d", m.Sessions.Rows, m.Sessions.Skipped)
	}
	if m.Sessions.Rows[0].ID != "20260202-000000-bb" || m.Sessions.Rows[1].ID != "20260101-000000-aa" {
		t.Errorf("order=%v", m.Sessions.Rows)
	}
	r := m.Sessions.Rows[0]
	if r.Count != 4 || r.Prompt != "second run\nmultiline" || r.When.Day() != 2 {
		t.Errorf("row=%+v", r)
	}
	if m.Sessions.Skipped != 1 {
		t.Errorf("skipped=%d, want 1", m.Sessions.Skipped)
	}
}

func TestSessionsEmpty(t *testing.T) {
	m := submit(New(testCfg(), "test"), "/sessions")
	m.SessionsPath = t.TempDir()
	m = loadSessions(m)
	m.Width = 100
	if got := m.renderSessions(m.Width, 10); !strings.Contains(got, "no sessions yet") {
		t.Errorf("empty:\n%s", got)
	}
}

func TestSessionsOpen(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	um, _ := m.Update(m.openSession("20260101-000000-aa")())
	m = um.(Model)
	if m.Sessions.Detail == nil || m.Sessions.Detail.ID != "20260101-000000-aa" {
		t.Fatalf("detail=%+v", m.Sessions.Detail)
	}
	if len(m.Sessions.Detail.Events) != 3 {
		t.Errorf("events=%d", len(m.Sessions.Detail.Events))
	}
	m.Width = 100
	got := m.renderSessions(m.Width, 12)
	if !strings.Contains(got, "first run") || !strings.Contains(got, "esc closes detail") {
		t.Errorf("viewer:\n%s", got)
	}
}

func TestSessionsOpenMissing(t *testing.T) {
	m := sessionsModel(t)
	um, _ := m.Update(m.openSession("nope")())
	m = um.(Model)
	if m.Sessions.Detail != nil {
		t.Error("missing must not open")
	}
	if a := lastAlert(m); a.Kind != "err" || !strings.Contains(a.Text, "nope") {
		t.Errorf("alert=%+v", a)
	}
}

func TestSessionsTarget(t *testing.T) {
	m := sessionsModel(t)
	cmd := m.submit("/sessions 20260101-000000-aa")
	if m.Tabs[m.Active].at.view != ViewSessions {
		t.Fatalf("view=%q", m.Tabs[m.Active].at.view)
	}
	if cmd == nil {
		t.Fatal("target must load the stream")
	}
	um, _ := m.Update(cmd())
	m = um.(Model)
	if m.Sessions.Detail == nil || m.Sessions.Detail.ID != "20260101-000000-aa" {
		t.Errorf("detail=%+v", m.Sessions.Detail)
	}
}

func TestSessionsEnterOpens(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	m.Sessions.Sel = 1
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("enter must load the stream")
	}
	um, _ := m.Update(cmd())
	m = um.(Model)
	if m.Sessions.Detail == nil || m.Sessions.Detail.ID != "20260101-000000-aa" {
		t.Errorf("detail=%+v", m.Sessions.Detail)
	}
}

func TestSessionsEscLadder(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	um, _ := m.Update(m.openSession("20260202-000000-bb")())
	m = um.(Model)
	m = key(m, "esc")
	if m.Sessions.Detail != nil || !m.sessionsActive() {
		t.Fatal("first esc must close the viewer, stay in list")
	}
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Errorf("second esc must reach chat: %q", m.Tabs[m.Active].at.view)
	}
}

func TestSessionsSelects(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "up")
	if m.Sessions.Sel != 1 {
		t.Errorf("sel=%d, want 1", m.Sessions.Sel)
	}
}

func TestSessionsRefreshKey(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	before := m.Sessions.Sel
	m = key(m, "r")
	if m.Sessions.Sel != before || m.Input.Value() != "" {
		t.Error("r must refresh without touching selection or input")
	}
	m.Input.SetValue("hi")
	m = key(m, "r")
	if m.Input.Value() != "hir" {
		t.Errorf("r with text must type: %q", m.Input.Value())
	}
}

func TestSessionsKeepSelection(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	m.Sessions.Sel = 1
	m = loadSessions(m)
	if m.Sessions.Sel != 1 {
		t.Errorf("sel=%d, want 1", m.Sessions.Sel)
	}
}

func TestSessionsViewerScroll(t *testing.T) {
	m := sessionsModel(t)
	var events []session.Event
	for i := 0; i < 50; i++ {
		events = append(events, session.Event{Kind: "reply", Text: "line"})
	}
	m.Sessions.Detail = &SessionDetail{ID: "big", Events: events}
	m.Width = 100
	m = key(m, "down")
	if m.Sessions.Detail.Scroll != 1 {
		t.Errorf("scroll=%d", m.Sessions.Detail.Scroll)
	}
	for i := 0; i < 200; i++ {
		m = key(m, "down")
	}
	mm := m
	body := sessionDisplay(mm.Sessions.Detail, BoxInner(mm.Width))
	if mm.Sessions.Detail.Scroll > len(body) {
		t.Errorf("scroll escaped: %d > %d", mm.Sessions.Detail.Scroll, len(body))
	}
	m = key(m, "up")
	if m.Sessions.Detail.Scroll >= 200 {
		t.Error("up must move back")
	}
}

func TestHistoryFromEvents(t *testing.T) {
	events := []session.Event{
		{Kind: "prompt", Text: "q1"},
		{Kind: "tool", Text: "t"},
		{Kind: "reply", Text: "stream"},
		{Kind: "result", Text: "r"},
		{Kind: "done", Text: "a1"},
		{Kind: "compact", Text: "c"},
		{Kind: "prompt", Text: "q2"},
		{Kind: "done", Text: "a2"},
		{Kind: "prompt", Text: "q3"},
	}
	got := historyFromEvents(events)
	// Reconstructed compaction retires old optional replies; raw observations remain conservative.
	want := [][2]string{{"user", "q1"}, {"user", "r"}, {"user", session.SummaryPrefix + "c"}, {"user", "q2"}, {"assistant", "a2"}, {"user", "q3"}}
	if len(got) != len(want) {
		t.Fatalf("history=%+v", got)
	}
	for i, w := range want {
		if got[i].Role != w[0] || got[i].Content != w[1] {
			t.Errorf("msg %d = %+v, want %v", i, got[i], w)
		}
	}
	if got := historyFromEvents(nil); len(got) != 0 {
		t.Errorf("empty=%+v", got)
	}
	var big []session.Event
	for i := 0; i < 60; i++ {
		big = append(big, session.Event{Kind: "prompt", Text: "q"}, session.Event{Kind: "done", Text: "a"})
	}
	if got := historyFromEvents(big); len(got) != 120 || got[0].Content != "q" || got[len(got)-1].Content != "a" {
		t.Errorf("trimmed=%d", len(got))
	}
}

// stubTerminal captures the spawn without opening windows.
func stubTerminal(t *testing.T, err error) *[]string {
	t.Helper()
	var got []string
	old := terminalOpen
	t.Cleanup(func() { terminalOpen = old })
	terminalOpen = func(argv []string) error {
		got = append([]string(nil), argv...)
		return err
	}
	return &got
}

func TestSessionsResumeSpawns(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	um, _ := m.Update(m.openSession("20260202-000000-bb")())
	m = um.(Model)
	got := stubTerminal(t, nil)
	tabs := len(m.Tabs)
	m = key(m, "enter")
	want := []string{"tui", "--config", m.CfgPath, "--resume", "20260202-000000-bb"}
	if len(*got) != len(want)+1 || (*got)[0] == "" {
		t.Fatalf("argv=%v", *got)
	}
	for i, w := range want {
		if (*got)[i+1] != w {
			t.Errorf("argv[%d]=%q, want %q", i+1, (*got)[i+1], w)
		}
	}
	if len(m.Tabs) != tabs || len(m.Chat.History) != 0 || m.Sessions.Detail == nil {
		t.Error("spawn must not touch tabs, history, or viewer")
	}
	if a := lastAlert(m); !strings.Contains(a.Text, "resumed 20260202-000000-bb in a new terminal") {
		t.Errorf("alert=%+v", a)
	}
}

func TestSessionsResumeErr(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	um, _ := m.Update(m.openSession("20260202-000000-bb")())
	m = um.(Model)
	stubTerminal(t, errTest)
	m = key(m, "enter")
	if a := lastAlert(m); a.Kind != "err" {
		t.Errorf("spawn failure must err: %+v", a)
	}
}

func TestBaselineHistory(t *testing.T) {
	m := sessionsModel(t)
	got, err := BaselineHistory(m.SessionsPath, "20260101-000000-aa")
	if err != nil || len(got) != 3 || got[0].Role != "user" || got[1].Content != "r" || got[2].Content != "final" {
		t.Errorf("baseline=%+v err=%v", got, err)
	}
	if _, err := BaselineHistory(m.SessionsPath, "nope"); err == nil {
		t.Error("missing must refuse")
	}
	if _, err := BaselineHistory(m.SessionsPath, "zzz"); err == nil || !strings.Contains(err.Error(), "corrupt line") {
		t.Errorf("corrupt must report the breakage: %v", err)
	}
	empty := filepath.Join(m.SessionsPath, "empty.jsonl")
	if err := os.WriteFile(empty, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := BaselineHistory(m.SessionsPath, "empty"); err == nil {
		t.Error("empty must refuse")
	}
}

func TestTerminalCommand(t *testing.T) {
	argv := []string{"/bin/harness", "tui", "--resume", "a b'c"}
	if runtime.GOOS == "windows" {
		t.Setenv("WT_SESSION", "")
		if _, _, err := terminalCommand(argv); err == nil || !strings.Contains(err.Error(), "resume with:") {
			t.Errorf("no Windows Terminal must refuse with the command: %v", err)
		}
		t.Setenv("WT_SESSION", "{any}")
		bin, args, err := terminalCommand(argv)
		if err != nil || bin != "wt.exe" || len(args) != 8 || args[0] != "-w" || args[1] != "0" || args[2] != "nt" || args[3] != "-d" || args[4] != cwd() || args[7] != `"a b'c"` {
			t.Errorf("wt=%q %v %v", bin, args, err)
		}
		return
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	bin, args, err := terminalCommand(argv)
	if err != nil || bin != "osascript" || !strings.Contains(args[1], "iTerm2") || !strings.Contains(args[1], "'a b'\\''c'") {
		t.Errorf("iterm=%q %v %v", bin, args, err)
	}
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if bin, _, err := terminalCommand(argv); err != nil || bin != "osascript" {
		t.Errorf("terminal=%q %v", bin, err)
	}
	t.Setenv("TERM_PROGRAM", "ghostty")
	if _, _, err := terminalCommand(argv); err == nil || !strings.Contains(err.Error(), "resume with:") {
		t.Errorf("foreign terminal must refuse with the command: %v", err)
	}
}

func TestTitleTurnFires(t *testing.T) {
	m := New(testCfg(), "test")
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Running = true
	m.Chat.Seq = 3
	if cmd := m.onRunDone(runDoneMsg{seq: 3, final: "done"}); cmd == nil {
		t.Error("every turn must title")
	}
	m.Cfg.Tabs.TitleEvery = 0
	m.Chat.Running = true
	if cmd := m.onRunDone(runDoneMsg{seq: 3, final: "done"}); cmd != nil {
		t.Error("disabled must not title")
	}
	m.Cfg.Tabs.TitleEvery = 5
	m.Chat.Running = true
	m.Chat.Turns = 0
	if cmd := m.onRunDone(runDoneMsg{seq: 3, final: "done"}); cmd != nil {
		t.Error("turn 1 of 5 must not title")
	}
}

func TestTitleTurnSkipsStop(t *testing.T) {
	m := New(testCfg(), "test")
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Running = true
	m.Chat.Seq = 3
	m.Chat.Turns = 4
	m.Chat.stopFlag = true
	if cmd := m.onRunDone(runDoneMsg{seq: 3, final: "done"}); cmd != nil {
		t.Error("stopped runs must not title")
	}
}

func TestSanitizeTitle(t *testing.T) {
	for in, want := range map[string]string{
		`"Ocean Poem"`:           "Ocean Poem",
		"  spaced out  ":         "spaced out",
		"first\nsecond":          "first",
		"":                       "",
		"***":                    "",
		"a very long title here": "a very long title here",
	} {
		if got := sanitizeTitle(in); got != want {
			t.Errorf("sanitize(%q)=%q, want %q", in, got, want)
		}
	}
	if got := sanitizeTitle(strings.Repeat("x", 60)); len([]rune(got)) != 28 {
		t.Errorf("long title=%d runes", len([]rune(got)))
	}
}

func TestOnTitle(t *testing.T) {
	m := New(testCfg(), "test")
	m.Tabs[m.Active].Title = "old"
	um, _ := m.Update(titleMsg{title: "  New Name  ", origin: m.origin()})
	m = um.(Model)
	if m.Tabs[m.Active].Title != "New Name" {
		t.Errorf("title=%q", m.Tabs[m.Active].Title)
	}
	um, _ = m.Update(titleMsg{title: "***", origin: m.origin()})
	m = um.(Model)
	if m.Tabs[m.Active].Title != "New Name" {
		t.Errorf("blank must keep: %q", m.Tabs[m.Active].Title)
	}
}

func TestTabBarShowsTitle(t *testing.T) {
	m := New(testCfg(), "test")
	m.Width = 120
	m.Tabs[m.Active].Title = "Ocean Poem"
	if got := m.View(); !strings.Contains(got, "Ocean Poem") {
		t.Error("tab bar omits the title")
	}
}

func TestSessionsRenderList(t *testing.T) {
	m := loadSessions(sessionsModel(t))
	m.Width = 100
	got := m.renderSessions(m.Width, 12)
	for _, want := range []string{"20260202-000000-bb", "second run", "4 events", "unreadable", "enter open", "r refresh", "esc chat"} {
		if !strings.Contains(got, want) {
			t.Errorf("list omits %q", want)
		}
	}
}
