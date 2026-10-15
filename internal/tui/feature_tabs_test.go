package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/tools/ask"
)

func featureIndex(m Model, v ViewID) int {
	for i, t := range m.Tabs {
		if t.feature == v {
			return i
		}
	}
	return -1
}

func TestFeatureTabsRetainWorkspacePagesAndChatOwner(t *testing.T) {
	m := turnsFixture(80, 24)
	owner := m.Chat.ID
	context := m.Chat.Context.Clone()
	for _, command := range []string{"/settings", "/status", "/files", "/plans", "/sessions", "/decisions", "/workflows"} {
		m = submit(m, command)
	}
	if len(m.Tabs) != 8 || m.chatTabIndex() != 0 {
		t.Fatal("functions replaced the conversation rather than retaining tabs")
	}
	m = submit(m, "/settings")
	m.Settings.Sel = 10
	m.Input.SetValue("settings draft")
	m.switchTab(featureIndex(m, ViewFiles))
	m.Files.Path = "source.go"
	m.Files.Viewer = &FileView{Title: "source.go", Logical: strings.Split(strings.Repeat("retained file\n", 60), "\n"), Scroll: 20}
	m.Input.SetValue("file draft")
	m.switchTab(featureIndex(m, ViewSettings))
	if m.Settings.Sel != 10 || m.Input.Value() != "file draft" {
		t.Fatal("Settings lost selection/draft")
	}
	m = submit(m, "/files")
	if m.Files.Viewer == nil || m.Files.Viewer.Scroll != 20 || m.Input.Value() != "" {
		t.Fatal("reopening Files reset viewer/draft")
	}
	if m.Chat.ID != owner || !reflect.DeepEqual(m.Chat.Context, context) {
		t.Fatal("inspector created or changed conversation authority")
	}
	m = submit(m, "/chat")
	if m.Active != 0 || len(m.Tabs) != 8 || m.Chat.ID != owner {
		t.Fatal("/chat destroyed feature tabs or changed owner")
	}
	m.newTab()
	second := m.Chat.ID
	m = submit(m, "/settings")
	m = submit(m, "/chat")
	if m.Chat.ID != second || m.Active == 0 {
		t.Fatal("workspace feature returned to originally opened chat instead of selected owner")
	}
}

func TestFeatureTabsBrowseAndCloseDuringStreamWithoutOrphan(t *testing.T) {
	m := New(testCfg(), "tabs")
	owner := m.Chat.ID
	m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 5
	m = submit(m, "/settings")
	m = submit(m, "/status")
	if !m.serveActive() || len(m.Tabs) != 3 || m.Chat.ID != owner {
		t.Fatal("could not inspect workspace during owned run")
	}
	m = viewportUpdate(m, tokenMsg{seq: 5, delta: "STREAM_STAYS_WITH_OWNER"})
	if m.Chat.Stream.String() != "STREAM_STAYS_WITH_OWNER" || m.Tabs[0].chat.Seq != 5 || !m.Tabs[0].chat.Running {
		t.Fatal("stream/mirror orphaned")
	}
	m = viewportKey(m, tea.KeyCtrlW)
	if m.Active != 0 || !m.Chat.Running || m.Chat.ID != owner || len(m.Tabs) != 2 {
		t.Fatal("closing inspector affected model run")
	}
	m = submit(m, "/settings")
	origin := m.origin()
	m.onTitle(titleMsg{title: "Owned conversation", origin: origin})
	if m.Tabs[0].Title != "Owned conversation" || m.Tabs[m.Active].Title != "" {
		t.Fatal("late title renamed feature tab")
	}
	m = viewportUpdate(m, runDoneMsg{seq: 5, final: "settled"})
	if m.Chat.Running || !m.settingsActive() || m.Tabs[0].chat.Running {
		t.Fatal("completion did not settle canonical owner in inspector")
	}
	m = submit(m, "/chat")
	if m.Chat.ID != owner || !strings.Contains(ansi.Strip(m.View()), "settled") {
		t.Fatal("return lost completed output")
	}
}

func TestFeatureTabsModalEditingNewAndHistoryOwnership(t *testing.T) {
	m := New(testCfg(), "tabs")
	m = submit(m, "/settings")
	f := settingFields()[1]
	m.armEdit(f)
	m.Input.SetValue("unapplied model name")
	oldName := m.Cfg.Model.Name
	m.switchTab(m.chatTabIndex())
	if strings.Contains(ansi.Strip(m.View()), "Enter applies edit") || strings.Contains(ansi.Strip(m.View()), "enter applies, esc cancels") {
		t.Fatal("dormant edit guidance leaked into Chat")
	}
	m.ModelName = ""
	m = submit(m, "ordinary chat text")
	if m.Cfg.Model.Name != oldName {
		t.Fatal("dormant edit consumed another tab input")
	}
	m.switchTab(featureIndex(m, ViewSettings))
	if m.Settings.Editing == nil || m.Input.Value() != "unapplied model name" {
		t.Fatal("armed feature edit not retained")
	}
	m.Settings.Editing = nil
	m = submit(m, "/new")
	id := m.Chat.ID
	m = submit(m, "/chat")
	if m.Chat.ID != id || m.Tabs[m.Active].chat.ID != id {
		t.Fatal("/new from feature resurrected old conversation")
	}
	m = submit(m, "/settings")
	m = submit(m, "/files")
	m = viewportKey(m, tea.KeyCtrlO)
	if !m.settingsActive() {
		t.Fatal("back did not return to retained Settings")
	}
	m = viewportKey(m, tea.KeyCtrlG)
	if !m.filesActive() {
		t.Fatal("forward did not return to retained Files")
	}
	m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "Preserve this answer?"}}, FreeDraft: new(string)}
	m.Input.SetValue("answer draft")
	active := m.Active
	m.switchTab(m.chatTabIndex())
	if m.Active != active || m.Input.Value() != "answer draft" || m.Chat.Dialog == nil {
		t.Fatal("tab switch discarded question/draft")
	}
}

func TestFeatureTabsDiffBookmarkUsesSequenceAndIndependentViewer(t *testing.T) {
	m := historyFixture(80, 24)
	m = submit(m, "/changes space name.go")
	m.Diff.Sel = 2
	seq := m.diffCur().Seq
	m.Diff.Viewer = &DiffViewer{Ev: *m.diffCur(), Scroll: 2}
	m = submit(m, "/diff")
	m.Diff.Sel = 10
	m.Diff.Viewer = &DiffViewer{Ev: m.Diff.Events[10], Scroll: 7}
	// Ring shifts while a sibling inspector is selected.
	m.Diff.Events = m.Diff.Events[1:]
	m.switchTab(featureIndex(m, ViewChanges))
	if m.diffCur().Seq != seq || m.Diff.Viewer == nil || m.Diff.Viewer.Scroll != 2 || m.Diff.Filter != "space name.go" {
		t.Fatal("changes bookmark reused another inspector or index")
	}
	m.switchTab(featureIndex(m, ViewDiff))
	if m.Diff.Viewer == nil || m.Diff.Viewer.Scroll != 7 {
		t.Fatal("diff viewer overwritten by Changes")
	}
}

func TestFeatureTabsChangesCommandPreservesOutgoingDiffAndExplicitScope(t *testing.T) {
	m := historyFixture(80, 24)
	m = submit(m, "/diff")
	m.Diff.Sel = 10
	seq := m.diffCur().Seq
	m.Diff.Viewer = &DiffViewer{Ev: *m.diffCur(), Scroll: 7}
	m = submit(m, "/changes space name.go")
	m.switchTab(featureIndex(m, ViewDiff))
	if m.diffCur().Seq != seq || m.Diff.Viewer == nil || m.Diff.Viewer.Scroll != 7 {
		t.Fatal("Changes command destroyed outgoing Diff viewer")
	}
	m = submit(m, "/changes space name.go")
	m = submit(m, "/chat")
	m = submit(m, "/changes")
	if m.Diff.Filter != "" || m.Tabs[m.Active].at.target != "" || m.Diff.Viewer != nil {
		t.Fatal("bare Changes command retained stale path scope")
	}
	m = submit(m, "/changes space name.go")
	m = submit(m, "/changes")
	m.goBack()
	if m.Diff.Filter != "space name.go" {
		t.Fatal("explicit empty Changes scope skipped previous scope in history")
	}
}

func TestFeatureTabsSettingsDisarmRestoresOrdinaryDraft(t *testing.T) {
	for _, action := range []string{"cancel", "apply", "slash"} {
		m := New(testCfg(), "tabs")
		m.navigate(ViewSettings, "")
		m.Input.SetValue("ordinary draft")
		m.armEdit(settingFields()[1])
		m.Input.SetValue("pending model")
		m.switchTab(m.chatTabIndex())
		m.switchTab(featureIndex(m, ViewSettings))
		switch action {
		case "cancel":
			m = viewportKey(m, tea.KeyEsc)
		case "apply":
			m.applyEdit("replacement model")
		case "slash":
			m = submit(m, "/status")
		}
		m.switchTab(m.chatTabIndex())
		if m.Input.Value() != "ordinary draft" || m.Settings.Editing != nil {
			t.Fatalf("%s lost ordinary draft or retained stale edit", action)
		}
	}
}

func TestFeatureTabsOverflowAndClick(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 12}, {34, 20}} {
		m := viewportUpdate(New(testCfg(), "tabs"), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, v := range []ViewID{ViewSettings, ViewStatus, ViewFiles, ViewPlans, ViewSessions, ViewDecisions, ViewWorkflows} {
			m.navigate(v, "")
		}
		frame := viewportFrame(t, m)
		if !strings.Contains(frame, "Workflows") && size[0] >= 80 {
			t.Fatal("overflow hid active tab")
		}
		m = submit(m, "/chat")
		content := m
		content.Width = max(1, m.Width-ansi.StringWidth(m.activityLabel())-1)
		_, spans := content.tabStrip()
		for _, s := range spans {
			if m.Tabs[s.index].feature == ViewSettings {
				m = viewportUpdate(m, tea.MouseMsg{Y: 0, X: s.start, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
				if !m.settingsActive() {
					t.Fatal("tab label click not routed")
				}
				break
			}
		}
	}
}

func TestFeatureTabsLateInspectorReadDoesNotReopenOrSwitchChat(t *testing.T) {
	m := New(testCfg(), "tabs")
	m = submit(m, "/files")
	m.Files.Root = "owned"
	m.Files.Path = "late.go"
	m.Files.Ticket = 9
	owner := m.Chat.ID
	m.closeTab()
	m = viewportUpdate(m, filesOpenedMsg{root: "owned", title: "late.go", ticket: 9, lines: []string{"retained source"}})
	if len(m.Tabs) != 1 || m.Chat.ID != owner || m.Active != 0 {
		t.Fatal("late inspector reopened tab or changed conversation")
	}
	m = submit(m, "/files")
	if m.Files.Viewer == nil || m.Files.Viewer.Logical[0] != "retained source" {
		t.Fatal("retained singleton read lost")
	}
}
