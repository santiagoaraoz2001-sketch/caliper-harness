package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/serve"
	"specific-harness/internal/watch"
)

func TestComposerOnePromptForMultilineAndWrapping(t *testing.T) {
	for _, text := range []string{"first\nsecond\nthird", strings.Repeat("long message ", 20), ""} {
		m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: 80, Height: 24})
		m.Input.SetValue(text)
		m.resizeComposer()
		v := ansi.Strip(m.composerView())
		if got := strings.Count(v, "❯"); got != 1 {
			t.Fatalf("got%d composer prompts for %q", got, text)
		}
	}
}

func TestAmbientDiffBurstCannotFloodChat(t *testing.T) {
	for _, running := range []bool{false, true} {
		m := scrollFixture()
		m.Chat.Running = running
		m.Diff.Max = 100
		m.scrollChat(-8)
		anchor := m.Chat.readOrigin
		before := len(m.Chat.Lines)
		for i := 0; i < 2000; i++ {
			m = viewportUpdate(m, diffEventMsg{ev: watch.Event{Seq: i, Path: fmt.Sprintf("verification/log-%d.txt", i), Op: watch.OpModify, BeforeKnown: true, Added: []string{"capture"}, AddedN: 1, After: []string{"capture"}, At: time.Now()}})
		}
		if len(m.Chat.Lines) > before+1 {
			t.Fatalf("ambient burst added%d transcript entries", len(m.Chat.Lines)-before)
		}
		if len(m.Diff.Events) != 100 || m.Diff.Events[99].Seq != 1999 {
			t.Fatal("capture ring lost existing identities")
		}
		if !m.Chat.pinned || m.Chat.readOrigin != anchor {
			t.Fatal("burst displaced reading identity")
		}
		m.Chat.unpin()
		v := ansi.Strip(m.renderChat(80, 40))
		if !strings.Contains(v, "2000 observed changes") || strings.Count(v, "capture expired") != 0 {
			t.Fatal("burst disclosure missing or expired-marker flood remains")
		}
		m.Chat.appendLine(lineAssistant, "Important agent response")
		m = viewportUpdate(m, diffEventMsg{ev: watch.Event{Seq: 2000, Path: "source.go", Op: watch.OpModify, After: []string{"new source"}, At: time.Now()}})
		if m.Chat.Lines[before+1].Text != "Important agent response" {
			t.Fatal("aggregation swallowed or reindexed agent response")
		}
	}
}

func TestMouseWheelScrollsVisibleFileAndJourneyWithDraft(t *testing.T) {
	m := journeyFixture(80, 24)
	m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: "brief"}})
	m.Input.SetValue("retained draft")
	m = viewportUpdate(m, tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 3, Button: tea.MouseButtonWheelDown})
	if m.Tabs[m.Active].at.journey.Scroll == 0 || m.Input.Value() != "retained draft" {
		t.Fatal("wheel did not scroll visible journey independently of draft")
	}
	m.navigate(ViewFiles, "")
	m.Files.Viewer = &FileView{Logical: strings.Split(strings.Repeat("file row\n", 100), "\n")}
	m = viewportUpdate(m, tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 3, Button: tea.MouseButtonWheelDown})
	if m.Files.Viewer.Scroll == 0 || m.Input.Value() != "retained draft" {
		t.Fatal("wheel did not scroll file independently of draft")
	}
}

func TestBrandIdentityUsesMintAndBlueprintStates(t *testing.T) {
	if PathSt.GetForeground() != TabActive.GetForeground() || InfoSt.GetForeground() != TabActive.GetForeground() {
		t.Fatal("generic hierarchy still uses borrowed blue/cyan")
	}
	if ErrHue != "#FF7884" || WarnHue != "#F2B84B" {
		t.Fatal("body states diverge from existing Blueprint activity palette")
	}
}

func TestScrollbarThumbDragKeepsGrabAndRelease(t *testing.T) {
	m := scrollFixture()
	m.pageChat(-1)
	lines := strings.Split(ansi.Strip(m.renderChat(m.Width, viewHeight(m))), "\n")
	y := -1
	for i, line := range lines {
		if strings.HasSuffix(line, "|") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatal("long chat has no visible thumb")
	}
	before := m.Chat.pinTop
	m = viewportUpdate(m, tea.MouseMsg{X: m.Width - 1, Y: len(m.tabChrome()) + y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.Chat.pinTop != before {
		t.Fatal("pressing thumb jumped reading position")
	}
	m = viewportUpdate(m, tea.MouseMsg{X: m.Width - 2, Y: len(m.tabChrome()) + y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if m.Chat.pinTop != before {
		t.Fatal("horizontal-only drag jumped reading position")
	}
	m = viewportUpdate(m, tea.MouseMsg{X: m.Width - 2, Y: len(m.tabChrome()) + y - 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if m.Chat.pinTop >= before {
		t.Fatal("horizontal drift stopped upward thumb drag")
	}
	m = viewportUpdate(m, tea.MouseMsg{X: m.Width - 2, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	before = m.Chat.pinTop
	m = viewportUpdate(m, tea.MouseMsg{X: m.Width - 2, Y: len(m.tabChrome()) + y + 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if m.Chat.pinTop != before {
		t.Fatal("released thumb still owns pointer")
	}
	if m.Input.Value() != "draft retained" {
		t.Fatal("scrollbar changed draft")
	}
}

func TestObservedCapturePinnedSourceAndNativeBoundary(t *testing.T) {
	m := scrollFixture()
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m.Chat.Running = true
	before := len(m.Chat.Lines)
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 9, Path: "source.go", BeforeKnown: true, Added: []string{"FIRST_CAPTURE_SOURCE"}, AddedN: 1}})
	rows := chatRows(&m, chatTextWidth(m.Width))
	for i, row := range rows {
		if row.origin.Part == "capture:9:added:0" {
			m.Chat.pinned = true
			m.Chat.pinTop = i
		}
	}
	m.captureChatAnchor()
	m = viewportUpdate(m, diffEventMsg{ev: watch.Event{Seq: 10, Path: "source.go", BeforeKnown: true, Added: []string{"SECOND_CAPTURE_SOURCE"}, AddedN: 1}})
	if m.Chat.Lines[before].Text != "9" || !strings.Contains(ansi.Strip(m.renderChat(80, viewHeight(m))), "FIRST_CAPTURE_SOURCE") {
		t.Fatal("later capture silently replaced pinned source")
	}
	m.Chat.unpin()
	m.Chat.Thoughts = map[int][]recordedThought{len(m.Chat.Lines): {{Label: "Thinking", Text: "INTERVENING_THOUGHT"}}}
	before = len(m.Chat.Lines)
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 11, Path: "later.go", BeforeKnown: true, Added: []string{"LATER_CAPTURE"}, AddedN: 1}})
	if len(m.Chat.Lines) != before+1 {
		t.Fatal("thought did not end observed burst")
	}
	joined := ""
	for _, row := range chatRows(&m, 76) {
		joined += row.text + "\n"
	}
	if strings.Index(joined, "INTERVENING_THOUGHT") > strings.Index(joined, "LATER_CAPTURE") {
		t.Fatal("capture displayed ahead of prior native thought")
	}
}

func TestStatusWheelScrollsPastSelectedRuntime(t *testing.T) {
	m := journeyFixture(80, 24)
	m.navigate(ViewManage, "")
	m.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "fixture", URL: "http://127.0.0.1:8080"}, Have: true, Health: serve.Health{Up: true}}}
	m.Serve.Warnings = []string{strings.Repeat("saved inventory detail ", 50) + "FINAL_STATUS_DETAIL"}
	m = viewportUpdate(m, tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 3, Button: tea.MouseButtonWheelDown})
	if m.Serve.Scroll == 0 {
		t.Fatal("Status wheel only moved selection, lower details unreachable")
	}
	for i := 0; i < 80; i++ {
		m = viewportUpdate(m, tea.MouseMsg{X: 8, Y: len(m.tabChrome()) + 3, Button: tea.MouseButtonWheelDown})
	}
	if !strings.Contains(ansi.Strip(m.View()), "FINAL_STATUS_DETAIL") || m.Serve.Sel != 0 {
		t.Fatal("wheel cannot reach bottom details with one selected runtime")
	}
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: 78, Height: 24})
	m = viewportUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(ansi.Strip(m.View()), "FINAL_STATUS_DETAIL") || m.Serve.Sel != 0 {
		t.Fatal("resize reset Status reading to the selected runtime")
	}
}
