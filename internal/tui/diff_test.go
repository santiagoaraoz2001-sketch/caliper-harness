package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/watch"
)

func diffModel() Model {
	m := New(testCfg(), "test")
	m.Width = 120
	m.Height = 40
	m.Diff = DiffState{Sel: -1, Max: 100, AttrWindow: time.Minute, Root: "root"}
	return m
}

func diffEv(path, op string, added ...string) watch.Event {
	return watch.Event{Path: path, Op: op, Added: added, AddedN: len(added), After: added}
}

func feedDiff(m Model, evs ...watch.Event) Model {
	for _, ev := range evs {
		m.onDiffEvent(diffEventMsg{ev: ev})
	}
	return m
}

func toDiff(m Model) Model {
	m.navigate(ViewDiff, "")
	return m
}

func TestDiffRing(t *testing.T) {
	m := diffModel()
	m.Diff.Max = 2
	m = feedDiff(m, diffEv("a", "create"), diffEv("b", "create"), diffEv("c", "create"))
	if len(m.Diff.Events) != 2 || m.Diff.Events[0].Path != "b" {
		t.Fatalf("ring=%+v", m.Diff.Events)
	}
	if m.Diff.Sel != 1 {
		t.Errorf("sel follows latest: %d", m.Diff.Sel)
	}
}

func TestDiffFollowStale(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("a", "create"), diffEv("b", "create"))
	m = key(toDiff(m), "up")
	m = feedDiff(m, diffEv("c", "create"))
	if m.Diff.Sel != 0 || len(m.Diff.Events) != 3 {
		t.Errorf("scrolled-up sel must hold: sel=%d n=%d", m.Diff.Sel, len(m.Diff.Events))
	}
}

func TestDiffAttribution(t *testing.T) {
	m := diffModel()
	m.lastTool, m.lastToolAt = "bash x", time.Now()
	m = feedDiff(m, diffEv("a", "modify"))
	if m.Diff.Events[0].AfterTool != "bash x" {
		t.Errorf("fresh tool must stamp: %+v", m.Diff.Events[0])
	}
	m.lastToolAt = time.Now().Add(-time.Hour)
	m = feedDiff(m, diffEv("b", "modify"))
	if m.Diff.Events[1].AfterTool != "" {
		t.Errorf("stale tool must not stamp: %+v", m.Diff.Events[1])
	}
}

func TestDiffNav(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("a", "create"), diffEv("b", "create"))
	m = key(toDiff(m), "up")
	if m.Diff.Sel != 0 {
		t.Errorf("up: %d", m.Diff.Sel)
	}
	m = key(m, "up")
	m = key(m, "down")
	m = key(m, "down")
	if m.Diff.Sel != 1 {
		t.Errorf("clamp: %d", m.Diff.Sel)
	}
}

func TestDiffViewer(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("a.txt", "modify", "one", "two"))
	m = enter(toDiff(m))
	if m.Diff.Viewer == nil {
		t.Fatal("enter must open the viewer")
	}
	if v := m.View(); !strings.Contains(v, "two") {
		t.Errorf("viewer omits file body:\n%s", v)
	}
	m = key(m, "esc")
	if m.Diff.Viewer != nil {
		t.Fatal("esc must close the viewer")
	}
	m = key(m, "esc")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Error("second esc must land in chat")
	}
}

func TestDiffComment(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("sub/a.txt", "modify", "x"))
	m = key(toDiff(m), "f")
	if m.Tabs[m.Active].at.view != ViewDiff {
		t.Fatal("f must preserve captured viewer")
	}
	if got := m.Input.Value(); got != "/comment sub/a.txt " {
		t.Fatalf("prefill=%q", got)
	}
	m = submit(m, m.Input.Value()+"needs review")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "sub/a.txt" || m.Comment[0].Text != "needs review" {
		t.Errorf("comment=%+v", m.Comment)
	}
}

func TestDiffEnterWithTextFallsThrough(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("a", "create"))
	m = toDiff(m)
	m.Input.SetValue("find")
	_, _, done := m.diffKey(tea.KeyMsg{Type: tea.KeyEnter})
	if done {
		t.Error("enter with text must fall through to submit")
	}
}

func TestDiffRender(t *testing.T) {
	m := diffModel()
	m.lastTool, m.lastToolAt = "bash x", time.Now()
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Path: "a.txt", Op: "modify",
		Added: []string{"new"}, AddedN: 3, Removed: []string{"old"}, RemovedN: 1, BeforeKnown: true}})
	m.onDiffEvent(diffEventMsg{ev: diffEv("b.txt", "create", "hi")})
	mv := m
	v := mv.renderDiff(120, 30)
	for _, want := range []string{"Diff", "a.txt", "b.txt", "❯", "↓", "enter open", "+ new", "- old", "after bash", "…2 more"} {
		if !strings.Contains(v, want) {
			t.Errorf("render omits %q:\n%s", want, v)
		}
	}
	if n := len(strings.Split(v, "\n")); n != 30 {
		t.Errorf("render is %d rows, want 30", n)
	}
}

func TestDiffEmpty(t *testing.T) {
	m := diffModel()
	if v := m.renderDiff(120, 20); !strings.Contains(v, "No file events") {
		t.Errorf("empty:\n%s", v)
	}
	m.Diff.WatchErr = "boom"
	if v := m.renderDiff(120, 20); !strings.Contains(v, "Watcher off: boom") {
		t.Errorf("watcherr:\n%s", v)
	}
}

func TestViewerScroll(t *testing.T) {
	var after []string
	for i := 0; i < 50; i++ {
		after = append(after, "l")
	}
	ev := watch.Event{Path: "big.txt", Op: "modify", Added: []string{"a", "b"}, AddedN: 2, After: after}
	m := feedDiff(diffModel(), ev)
	m = enter(toDiff(m))
	m.diffScroll(100, 30)
	avail := viewerAvail(ev, m.diffHeight(30, m.Width))
	if m.Diff.Viewer.Scroll != 50-avail {
		t.Errorf("scroll clamps to latest: %d", m.Diff.Viewer.Scroll)
	}
	if v := m.View(); !strings.Contains(v, "── file ──") {
		t.Errorf("viewer omits file section:\n%s", v)
	}
}

func TestDiffViewerCommentLines(t *testing.T) {
	m := feedDiff(diffModel(), diffEv("sub/a.txt", "modify", "x", "y"))
	m = enter(toDiff(m))
	if m.Diff.Viewer == nil {
		t.Fatal("enter must open the viewer")
	}
	m = submit(m, "/comment [2] second line")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "sub/a.txt" || m.Comment[0].Quote != "y" {
		t.Fatalf("comment=%+v", m.Comment)
	}
	m = submit(m, "/goto 2")
	if m.Diff.Viewer.Scroll != 0 || !strings.Contains(m.View(), "2  y") {
		t.Fatalf("scroll=%d", m.Diff.Viewer.Scroll)
	}
}
