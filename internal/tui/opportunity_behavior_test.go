package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/session"
	"specific-harness/internal/watch"
	"strings"
	"testing"
)

func TestPinnedStreamSurvivesWatcherAndFlush(t *testing.T) {
	m := scrollFixture()
	m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 4
	m.Chat.Stream.WriteString(strings.Repeat("stream line repeated words Unicode 界\n", 60))
	rows := chatRows(&m, chatTextWidth(m.Width))
	m.Chat.pinned = true
	for i, row := range rows {
		if row.origin.Stream && row.origin.Part == "assistant:line:20" {
			m.Chat.pinTop = i
			break
		}
	}
	m.captureChatAnchor()
	want := m.Chat.readOrigin
	next, _ := m.Update(diffEventMsg{ev: watch.Event{Seq: 40, Path: "watched.go", BeforeKnown: true, Added: []string{"new"}, AddedN: 1}})
	m = next.(Model)
	got := chatRows(&m, chatTextWidth(m.Width))[m.Chat.pinTop].origin
	if got != want {
		t.Fatalf("watch event moved streaming anchor: %+v want %+v", got, want)
	}
	m.flushStream()
	m.restoreChatAnchor()
	got = chatRows(&m, chatTextWidth(m.Width))[m.Chat.pinTop].origin
	if got.Stream || got.Part != want.Part || got.Offset != want.Offset {
		t.Fatal("flush lost streaming source position")
	}
}
func TestPinnedDiffSourceDoesNotAliasFooter(t *testing.T) {
	m := scrollFixture()
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 9, Path: "source.go", BeforeKnown: true, Added: []string{"added source row"}, AddedN: 11}})
	for i := 0; i < 15; i++ {
		m.Chat.appendLine(lineResult, fmt.Sprintf("trailing_%d", i))
	}
	rows := chatRows(&m, chatTextWidth(m.Width))
	m.Chat.pinned = true
	for i, row := range rows {
		if row.origin.Part == "capture:9:added:0" {
			m.Chat.pinTop = i
			break
		}
	}
	m.captureChatAnchor()
	want := m.Chat.readOrigin
	m.restoreChatAnchor()
	got := chatRows(&m, chatTextWidth(m.Width))[m.Chat.pinTop]
	if got.origin != want || !strings.Contains(got.text, "added source row") {
		t.Fatal("diff source aliases disclosure or action")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	got = chatRows(&m, chatTextWidth(m.Width))[m.Chat.pinTop]
	if got.origin.Part != want.Part {
		t.Fatal("resize lost source identity")
	}
}
func TestSessionPagingDoesNotSkipRecordedRows(t *testing.T) {
	m := scrollFixture()
	m.Chat = freshChatState()
	m.Input.SetValue("")
	m.navigate(ViewSessions, "")
	d := &SessionDetail{ID: "owned-test"}
	for i := 0; i < 80; i++ {
		d.Events = append(d.Events, session.Event{Kind: "result", Text: fmt.Sprintf("RECORDED_%03d", i)})
	}
	m.Sessions.Detail = d
	seen := ansi.Strip(m.View())
	for i := 0; i < 50; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = next.(Model)
		seen += "\n" + ansi.Strip(m.View())
	}
	for i := 0; i < 80; i++ {
		if !strings.Contains(seen, fmt.Sprintf("RECORDED_%03d", i)) {
			t.Fatalf("session page skipped event %d", i)
		}
	}
	if !strings.Contains(seen, "last recorded: result") || strings.Contains(seen, "qualified") {
		t.Fatal("recorded context invented qualification")
	}
}
func TestSyntaxUsesOriginalAcceptanceAndArgumentPrefix(t *testing.T) {
	m := New(testCfg(), "syntax")
	m.Input.SetValue("/auth ")
	if m.pickerUsage("all") != "/auth <yolo|risky|all>" {
		t.Fatal("argument prefix lost syntax")
	}
	m.Input.SetValue("/comm")
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Syntax: /comment [ref] [lines] <text>") {
		t.Fatal("selected command syntax missing")
	}
	m = key(m, "tab")
	if m.Input.Value() != "/comment " {
		t.Fatalf("syntax changed accepted input: %q", m.Input.Value())
	}
}
func TestOpportunityFramesKeepSelectionDetailsAndQuietControls(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := scrollFixture()
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = next.(Model)
		m.Chat = freshChatState()
		m.Input.SetValue("")
		m.navigate(ViewSettings, "")
		for i, f := range settingFields() {
			if f.Section == "params" && f.Key == "top_k" {
				m.Settings.Sel = i
			}
		}
		v := viewportFrame(t, m)
		for _, want := range []string{"top_k", "unknown", "not applied", "s save"} {
			if !strings.Contains(v, want) {
				t.Fatalf("selected setting missing %q", want)
			}
		}
		m.navigate(ViewWorkflows, "")
		m.Wf.Rows = []WfRow{{Title: "Owned approach", Done: 1, Total: 2, Unverified: 1, Failed: 1, Agents: 2, Step: "Inspect existing receipt."}}
		v = viewportFrame(t, m)
		if !strings.Contains(v, "1 failed") || strings.Contains(v, "0 cancelled") {
			t.Fatal("exception priority lost")
		}
		if !strings.Contains(v, "THINK:ON requested") || !strings.Contains(v, "last measured prompt unknown/") {
			t.Fatal("footer lost measurement semantics")
		}
	}
}

func TestHistoryPreviewCountsOnlyVisibleSource(t *testing.T) {
	m := New(testCfg(), "history")
	m.Diff.History = true
	m.Diff.Filter = "source.go"
	m.Diff.Events = []watch.Event{{Seq: 15, Path: "source.go", BeforeKnown: true, ReadError: "capture read refused", Removed: []string{"old1", "old2"}, Added: []string{"new1", "new2", "new3"}, RemovedN: 7, AddedN: 10}}
	m.Diff.Sel = 0
	for _, h := range []int{7, 8, 9, 12} {
		rows := m.historyLines(80, h)
		shown := 0
		for _, row := range rows {
			plain := ansi.Strip(row)
			if strings.HasPrefix(plain, "− ") || strings.HasPrefix(plain, "+ ") {
				shown++
			}
		}
		want := fmt.Sprintf("%d changed lines outside preview", 17-shown)
		if !strings.Contains(ansi.Strip(strings.Join(rows, "\n")), want) {
			t.Fatalf("height%d preview understates source omission: %v", h, rows)
		}
	}
}

func TestQueuedPlainNoteRetainsActualText(t *testing.T) {
	m := New(testCfg(), "comment")
	m.navigate(ViewFiles, "")
	m = submit(m, "/comment inspect these lines")
	m.navigate(ViewChat, "")
	if len(m.Comment) != 1 || m.Comment[0].Ref != "" || m.Comment[0].Text != "inspect these lines" {
		t.Fatal("comment payload changed")
	}
	if text := ansi.Strip(chipsView(m)); !strings.Contains(text, "inspect these lines") || strings.Contains(text, " · .") {
		t.Fatalf("plain queued note lost identity:%q", text)
	}
}
