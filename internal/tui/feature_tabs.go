package tui

import (
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/tui/thirdparty/textarea"
)

// Workspace inspectors are singletons. Their existing state owners remain on
// Model; tabs retain presentation, not copies of execution or conversation state.
type tabEditor struct {
	input          textarea.Model
	drafts         [3]string
	draft, histIdx int
	histDraft      string
	pickerHide     bool
}
type diffBookmark struct {
	seq    int
	have   bool
	viewer *DiffViewer
}

func (m *Model) ensureTabID(i int) {
	if m.Tabs[i].id == 0 {
		m.nextTab++
		m.Tabs[i].id = m.nextTab
	}
}
func (m Model) tabIndex(id uint64) int {
	for i, t := range m.Tabs {
		if t.id == id {
			return i
		}
	}
	return -1
}
func (m Model) chatTabIndex() int { return m.tabIndex(m.chatTab) }
func (m Model) chatTabCount() int {
	n := 0
	for _, t := range m.Tabs {
		if t.feature == "" {
			n++
		}
	}
	return n
}
func (m *Model) tabPlace() place {
	m.ensureTabID(m.Active)
	p := m.Tabs[m.Active].at
	p.tab = m.Tabs[m.Active].id
	return p
}
func (m *Model) saveTabPresentation() {
	s := &tabEditor{m.Input, m.Drafts, m.Draft, m.HistIdx, m.HistDraft, m.PickerHide}
	t := &m.Tabs[m.Active]
	if m.testsActive() && m.Tests.Editing {
		m.testsEditor = s
	} else if m.settingsActive() && m.Settings.Editing != nil {
		m.settingsEditor = s
	} else {
		if i := m.chatTabIndex(); i >= 0 {
			m.Tabs[i].editor = s
		}
	}
	if t.feature == ViewDiff || t.feature == ViewChanges {
		b := &diffBookmark{}
		if e := m.diffCur(); e != nil {
			b.seq, b.have = e.Seq, true
		}
		if m.Diff.Viewer != nil {
			v := *m.Diff.Viewer
			b.viewer = &v
		}
		t.diff = b
	}
}
func (m *Model) restoreTabEditor() {
	var s *tabEditor
	if i := m.chatTabIndex(); i >= 0 {
		s = m.Tabs[i].editor
	}
	if m.settingsActive() && m.Settings.Editing != nil {
		s = m.settingsEditor
	}
	if m.testsActive() && m.Tests.Editing {
		s = m.testsEditor
	}
	if s == nil {
		m.Input = newComposer()
		m.Drafts, m.Draft, m.HistIdx, m.HistDraft, m.PickerHide = [3]string{}, 0, 0, "", false
	} else {
		m.Input, m.Drafts, m.Draft, m.HistIdx, m.HistDraft, m.PickerHide = s.input, s.drafts, s.draft, s.histIdx, s.histDraft, s.pickerHide
	}
	m.PickerIdx = 0
	m.resizeComposer()
}
func (m *Model) restoreDiffBookmark() {
	b := m.Tabs[m.Active].diff
	if b == nil || !m.diffActive() {
		return
	}
	if b.have {
		for i, e := range m.Diff.Events {
			if e.Seq == b.seq && (m.Diff.Filter == "" || e.Path == m.Diff.Filter) {
				m.Diff.Sel = i
				break
			}
		}
	}
	if b.viewer == nil || m.Diff.Filter == "" || b.viewer.Ev.Path == m.Diff.Filter {
		m.Diff.Viewer = b.viewer
	}
}
func (m *Model) focusChat() {
	i := m.chatTabIndex()
	if i < 0 || i == m.Active {
		return
	}
	from := m.tabPlace()
	if !m.canSwitchTab(i) {
		return
	}
	m.switchTab(i)
	m.Tabs[i].back = append(m.Tabs[i].back, from)
	m.Tabs[i].fwd = nil
}
func (m *Model) focusFeature(v ViewID, target string) {
	if m.Chat.Dialog != nil {
		m.alert("info", "answer or dismiss the current dialog before opening another tab")
		return
	}
	from := m.tabPlace()
	i := -1
	for n, t := range m.Tabs {
		if t.feature == v {
			i = n
			break
		}
	}
	if i < 0 {
		m.nextTab++
		m.Tabs = append(m.Tabs, Tab{id: m.nextTab, feature: v, at: place{view: v, target: target}})
		i = len(m.Tabs) - 1
	}
	m.switchTab(i)
	t := &m.Tabs[i]
	t.back = append(t.back, from)
	t.fwd = nil
	if target != "" || v == ViewJourney {
		t.at = place{view: v, target: target}
	}
	m.Plans.Request++
	m.Journey.Request++
	m.syncDiffScope()
	m.syncJourneyFocus()
}
func (m *Model) walkTabHistory(forward bool) {
	from := m.tabPlace()
	t := &m.Tabs[m.Active]
	stack := t.back
	if forward {
		stack = t.fwd
	}
	if len(stack) == 0 {
		return
	}
	dest := stack[len(stack)-1]
	i := m.Active
	if dest.tab != 0 {
		i = m.tabIndex(dest.tab)
	}
	if i < 0 {
		if forward {
			t.fwd = t.fwd[:len(t.fwd)-1]
		} else {
			t.back = t.back[:len(t.back)-1]
		}
		m.walkTabHistory(forward) // Closed tabs are skipped, never resurrected.
		return
	}
	if !m.canSwitchTab(i) {
		return
	}
	if forward {
		t.fwd = t.fwd[:len(t.fwd)-1]
	} else {
		t.back = t.back[:len(t.back)-1]
	}
	if i != m.Active {
		m.switchTab(i)
	}
	t = &m.Tabs[m.Active]
	if forward {
		t.back = append(t.back, from)
	} else {
		t.fwd = append(t.fwd, from)
	}
	t.at = dest
	m.Plans.Request++
	m.Journey.Request++
	m.syncDiffScope()
	m.restoreDiffBookmark()
	m.syncJourneyFocus()
}

// Only labels receive click targets. Hidden-count hints and the running state
// remain informational; the original keyboard controls are unchanged.
func (m Model) tabAt(x int) int {
	content := m
	content.Width = max(1, m.Width-ansi.StringWidth(m.activityLabel())-1)
	_, spans := content.tabStrip()
	for _, s := range spans {
		if x >= s.start && x < s.end {
			return s.index
		}
	}
	return -1
}
