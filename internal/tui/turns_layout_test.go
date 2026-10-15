package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/watch"
)

func turnsFixture(w, h int) Model {
	m := viewportUpdate(New(testCfg(), "turns"), tea.WindowSizeMsg{Width: w, Height: h})
	for i := 0; i < 7; i++ {
		m.Chat.appendTurn(fmt.Sprintf("REQUEST_%d · preserve original commands 界", i))
		m.Chat.appendLine(lineAssistant, strings.Repeat(fmt.Sprintf("REPLY_%d retained evidence\n", i), 16))
		m.Chat.appendLine(lineTool, "read exact source")
		m.Chat.appendLine(lineResult, "recorded observation")
	}
	return m
}

func TestTurnsExactRelativeSyntaxAndLatest(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {40, 12}} {
		m := turnsFixture(sz[0], sz[1])
		before := m.Chat.Context.Clone()
		lines := append([]Line(nil), m.Chat.Lines...)
		m = submit(m, "/turns -5")
		if !m.Chat.pinned || m.Chat.readOrigin.Line != m.Chat.TurnStarts[1] || m.Chat.readOrigin.Part != lineUser {
			t.Fatalf("-5 failed to select fifth prior root prompt: %+v", m.Chat.readOrigin)
		}
		if !strings.Contains(viewportFrame(t, m), "REQUEST_1") {
			t.Fatal("selected request not visible")
		}
		m = submit(m, "/turns -5")
		if m.Chat.readOrigin.Line != m.Chat.TurnStarts[1] {
			t.Fatal("offset accumulated from reading cursor")
		}
		m = viewportUpdate(m, tea.WindowSizeMsg{Width: 78, Height: sz[1]})
		if m.Chat.readOrigin.Line != m.Chat.TurnStarts[1] {
			t.Fatal("resize lost turn anchor")
		}
		m.Chat.Running, m.Chat.Streaming = true, true
		m.Chat.Stream.WriteString("new streamed output below reading position")
		if !strings.Contains(viewportFrame(t, m), "REQUEST_1") {
			t.Fatal("stream displaced selected turn")
		}
		for _, command := range []string{"/turns", "/turns 0", "/turns current"} {
			m = submit(m, command)
			if m.Chat.pinned || !strings.Contains(viewportFrame(t, m), "new streamed output") {
				t.Fatalf("%s did not restore live tail", command)
			}
			m = submit(m, "/turns -5")
		}
		if !reflect.DeepEqual(before, m.Chat.Context) || !reflect.DeepEqual(lines, m.Chat.Lines) {
			t.Fatal("turn navigation mutated context/transcript")
		}
	}
}

func TestTurnsInvalidBoundsClearAndTabs(t *testing.T) {
	m := turnsFixture(80, 24)
	m = submit(m, "/turns -5")
	anchor := m.Chat.readOrigin
	for _, command := range []string{"/turns 1", "/turns nonsense", "/turns -1 extra", "/turns -7", "/turns -9223372036854775808", "/turns -999999999999999999999"} {
		m = submit(m, command)
		if m.Chat.readOrigin != anchor || len(m.Alerts) == 0 {
			t.Fatalf("invalid command moved cursor or silent: %s", command)
		}
		m.Alerts = nil
	}
	m = submit(m, "/settings")
	m = submit(m, "/turns -2")
	if m.Tabs[m.Active].at.view != ViewChat || m.Chat.readOrigin.Line != m.Chat.TurnStarts[4] {
		t.Fatal("feature command did not return to retained chat")
	}
	m.newTab()
	m = submit(m, "/turns -1")
	if m.Chat.pinned || len(m.Chat.TurnStarts) != 0 || len(m.Alerts) == 0 {
		t.Fatal("empty tab borrowed another conversation's turns")
	}
	m.Alerts = nil
	m.switchTab(0)
	m = submit(m, "/turns -5")
	if m.Chat.readOrigin.Line != m.Chat.TurnStarts[1] {
		t.Fatal("tab did not retain own boundaries")
	}
	m.clearChat()
	m = submit(m, "/turns -1")
	if len(m.Chat.TurnStarts) != 0 || m.Chat.pinned || len(m.Alerts) == 0 {
		t.Fatal("clear retained stale turn indices")
	}
	m.Chat.Context.Entries = []session.Entry{{Message: llm.Message{Role: "user", Content: "retained resumed prompt"}}}
	m.AddSysLine("resumed fixture · prompt size unknown")
	m.SyncTabChat()
	resumed := m.Chat.Context.Clone()
	m = submit(m, "/turns -1")
	if m.Chat.pinned || len(m.Chat.TurnStarts) != 0 || !reflect.DeepEqual(m.Chat.Context, resumed) {
		t.Fatal("resumed context invented visible turn rows or changed context")
	}
	m = submit(m, "/turns current")
	if m.Chat.pinned {
		t.Fatal("latest cannot return in empty/resumed view")
	}
}

func TestTurnsRootPromptAndInterjectionBoundaries(t *testing.T) {
	m := New(testCfg(), "turns")
	m.ModelName = "fixture"
	// Fail before inference. An accepted root prompt remains a visible turn even
	// when its ordinary owning store admission fails.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	m.StorePath = blocker
	m.startRun("btw: this is a real root prompt")
	if len(m.Chat.TurnStarts) != 1 || m.Chat.TurnStarts[0] != 0 {
		t.Fatal("accepted prompt did not create visible root boundary")
	}
	m.Chat.Running = true
	m.onEvent(eventMsg{seq: m.Chat.Seq, kind: "btw", text: "interjected comment"})
	m.onEvent(eventMsg{seq: m.Chat.Seq, kind: "tool", text: "read a source file"})
	if len(m.Chat.TurnStarts) != 1 {
		t.Fatal("interjection/tool created an extra turn")
	}
	m.Chat.Running = false
	m.retryChat()
	if len(m.Chat.TurnStarts) != 2 {
		t.Fatal("explicit retry did not create its own submitted turn")
	}
}

func TestAdaptiveSettingsLabelsAndCompactAncestry(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {34, 20}, {40, 12}} {
		m := journeyFixture(sz[0], sz[1])
		m.navigate(ViewSettings, "")
		for i, f := range settingFields() {
			if f.Key == "max_output_tokens" {
				m.Settings.Sel = i
			}
		}
		v := viewportFrame(t, m)
		if sz[0] == 120 && !strings.Contains(v, "❯ output tokens including reasoning (0 = runtime policy unknown)") {
			t.Fatal("wide Settings still clips label beside spare width")
		}
		m.navigate(ViewJourney, "task-one")
		m.Tabs[m.Active].at.journey = journeyFrame{Kind: rowTask}
		header := m.journeyHeader(m.Width)
		if sz[0] == 80 && (len(header) < 1 || !strings.Contains(ansi.Strip(header[0]), "P1 › P1-A1 › P1-A1-T1") || strings.Contains(ansi.Strip(header[0]), "Startup reliability")) {
			t.Fatal("narrow breadcrumb still repeats ancestor titles")
		}
		viewportFrame(t, m)
		m.Tabs[m.Active].at.journey.Kind = "brief"
		full := ansi.Strip(strings.Join(m.journeyDetailRows(m.Width), "\n"))
		for _, title := range []string{"Startup reliability", "Dependency checks", "Inspect startup checks"} {
			if !strings.Contains(full, title) {
				t.Fatalf("full ancestry lost: %s", title)
			}
		}
	}
	fields := []SettingField{{Label: strings.Repeat("界", 20)}}
	if got := settingLabelWidth(fields, 76); got != 40 {
		t.Fatalf("Unicode label width uses bytes instead of cells: %d", got)
	}
}

func TestFilteredChangesIdentityQualityAndPaging(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		m := historyFixture(sz[0], sz[1])
		m = submit(m, "/changes space name.go")
		v := viewportFrame(t, m)
		if strings.Count(v, "space name.go") != 1 || !strings.Contains(v, "captured source") {
			t.Fatal("filtered history repeats path or lacks quality")
		}
		stride := max(1, m.historyListHeight(m.diffHeight(viewHeight(m), m.Width)))
		old := m.Diff.Sel
		m = viewportKey(m, tea.KeyPgUp)
		if m.Diff.Sel == old && stride < 8 {
			t.Fatal("page did not move selection")
		}
		m = submit(m, "/open-diff")
		if m.Files.Capture == nil || m.Files.Viewer.Title != "space name.go" {
			t.Fatal("full file identity no longer accessible")
		}
		m = submit(m, "/changes")
		m = submit(m, "/changes other.go")
		viewportFrame(t, m)
		// Truly unfiltered multi-file history must retain per-row paths.
		m.navigate(ViewChanges, "")
		m.Tabs[m.Active].at.target = "" // Explicitly seed an unfiltered workspace list.
		m.syncDiffScope()
		m.Diff.Sel = len(m.Diff.Events) - 1
		all := ansi.Strip(strings.Join(m.historyLines(80, 40), "\n"))
		if !strings.Contains(all, "space name.go") || !strings.Contains(all, "other.go") {
			t.Fatal("multi-file rows lost identity")
		}
	}
	m := New(testCfg(), "quality")
	m.Diff.Filter = "file.go"
	m.Diff.History = true
	for i, ev := range []watch.Event{
		{ReadError: "read failed", BeforeKnown: true}, {Op: watch.OpDelete, BeforeKnown: true}, {Truncated: true, BeforeKnown: true}, {}, {BeforeKnown: true, BeforePartial: true},
	} {
		ev.Seq = i
		ev.Path = "file.go"
		ev.At = time.Now()
		m.Diff.Events = []watch.Event{ev}
		m.Diff.Sel = 0
		text := ansi.Strip(strings.Join(m.historyLines(120, 30), "\n"))
		if !strings.Contains(text, captureQuality(ev)) {
			t.Fatal("capture quality lost")
		}
	}
}

func TestAdaptiveSettingsWideLabels(t *testing.T) {
	m := viewportUpdate(New(testCfg(), "labels"), tea.WindowSizeMsg{Width: 120, Height: 40})
	m = submit(m, "/settings")
	for i, f := range settingFields() {
		if f.Key == "max_output_tokens" {
			m.Settings.Sel = i
		}
	}
	if !strings.Contains(viewportFrame(t, m), "❯ output tokens including reasoning (0 = runtime policy unknown)") {
		t.Fatal("wide labels remain hard-capped")
	}
}

func TestTurnsUserSurfaceDoesNotImplyTruncation(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		m := turnsFixture(size[0], size[1])
		m = submit(m, "/turns -5")
		for _, line := range strings.Split(viewportFrame(t, m), "\n") {
			if strings.Contains(line, "REQUEST_1") && strings.Contains(line, "…") {
				t.Fatal("scrollbar reservation truncated padding and added a false omission marker")
			}
		}
	}
}
