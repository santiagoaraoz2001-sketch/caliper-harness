package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// Projection-only reading identity; never serialized or included in a request.
type chatOrigin struct {
	Line          int
	Part          string
	Offset        int
	Stream, Valid bool
}

func chatIdentity(id string) string {
	if id == "" {
		return ""
	}
	return " · " + id
}
func chatKindLabel(kind string) string {
	switch kind {
	case lineUser:
		return "Your message"
	case lineAssistant:
		return "Assistant reply"
	case lineSys:
		return "Harness notice"
	}
	return "Recorded output"
}
func chatBodyHeight(h int) int { return max(1, h-1) }
func (m *Model) captureChatAnchor() {
	if !m.Chat.pinned {
		return
	}
	rows := chatRows(m, chatTextWidth(m.Width))
	if m.Chat.pinTop >= 0 && m.Chat.pinTop < len(rows) {
		m.Chat.readOrigin = rows[m.Chat.pinTop].origin
	}
}
func (m *Model) restoreChatAnchor() {
	if !m.Chat.pinned || !m.Chat.readOrigin.Valid {
		return
	}
	rows := chatRows(m, chatTextWidth(m.Width))
	target := m.Chat.readOrigin
	best := -1
	for i, row := range rows {
		o := row.origin
		if o.Line == target.Line && o.Part == target.Part && o.Stream == target.Stream {
			if best < 0 || o.Offset <= target.Offset {
				best = i
			}
			if o.Offset > target.Offset {
				break
			}
		}
	}
	if best >= 0 {
		m.Chat.pinTop = best
	}
	m.Chat.pinTop = min(max(0, m.Chat.pinTop), max(0, len(rows)-chatBodyHeight(viewHeight(*m))))
}
func (m *Model) scrollChat(delta int) {
	rows := chatRows(m, chatTextWidth(m.Width))
	h := viewHeight(*m)
	if len(rows) <= h {
		m.Chat.unpin()
		return
	}
	if !m.Chat.pinned {
		if delta >= 0 {
			return
		}
		m.Chat.pinTop = max(0, len(rows)-chatBodyHeight(h))
		m.Chat.pinned = true
	}
	m.Chat.pinTop = max(0, m.Chat.pinTop+delta)
	if delta > 0 && m.Chat.pinTop+chatBodyHeight(h) >= len(rows) {
		m.Chat.unpin()
		return
	}
	m.captureChatAnchor()
}
func (m *Model) pageChat(direction int) {
	m.scrollChat(direction * max(1, chatBodyHeight(viewHeight(*m))/2))
}
func (m Model) chatMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	top := len(m.tabChrome())
	bottom := top + viewHeight(m)
	if m.HelpOpen || m.Chat.Dialog != nil || m.Tabs[m.Active].at.view != ViewChat {
		return m, nil
	}
	if m.Chat.scrollDrag && msg.Action == tea.MouseActionMotion {
		total := len(chatRows(&m, chatTextWidth(m.Width)))
		page, height := chatBodyHeight(viewHeight(m)), viewHeight(m)
		_, thumb := chatTrackMetrics(total, page, 0, height)
		limit := max(0, total-page)
		position := m.Chat.scrollDragTop + (msg.Y-top-m.Chat.scrollDragY)*limit/max(1, height-thumb)
		m.setChatScrollTop(position, limit)
		return m, nil
	}
	if msg.Y < top || msg.Y >= bottom || msg.X < 0 || msg.X >= m.Width {
		return m, nil
	}
	if msg.X == m.Width-1 && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		total := len(chatRows(&m, chatTextWidth(m.Width)))
		page := chatBodyHeight(viewHeight(m))
		if total > viewHeight(m) {
			position := max(0, total-page)
			if m.Chat.pinned {
				position = m.Chat.pinTop
			}
			start, thumb := chatTrackMetrics(total, page, position, viewHeight(m))
			at := msg.Y - top
			m.Chat.scrollDrag = true
			m.Chat.scrollDragY, m.Chat.scrollDragTop = at, position
			if at < start || at >= start+thumb {
				m.seekChatTrack(at - thumb/2)
				m.Chat.scrollDragTop = max(0, total-page)
				if m.Chat.pinned {
					m.Chat.scrollDragTop = m.Chat.pinTop
				}
			}
		}
		return m, nil
	}
	if tea.MouseEvent(msg).IsWheel() {
		m.Chat.scrollDrag = false
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollChat(-2)
	case tea.MouseButtonWheelDown:
		m.scrollChat(2)
	}
	return m, nil
}

// Wheel input belongs to the visible body, independently of a retained draft.
// Reuse each view's scrolling/selection owner; no Enter/action keys are injected.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Y == 0 && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if i := m.tabAt(msg.X); i >= 0 {
			m.switchTab(i)
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionRelease {
		m.Chat.scrollDrag = false
		return m, nil
	}
	if m.Tabs[m.Active].at.view == ViewChat && !m.HelpOpen && m.Chat.Dialog == nil {
		return m.chatMouse(msg)
	}
	m.Chat.scrollDrag = false
	delta := 0
	if msg.Button == tea.MouseButtonWheelUp {
		delta = -2
	}
	if msg.Button == tea.MouseButtonWheelDown {
		delta = 2
	}
	if delta == 0 || msg.X < 0 || msg.X >= m.Width || msg.Y < len(m.tabChrome()) {
		return m, nil
	}
	if m.HelpOpen {
		if msg.Y < m.Height-1 {
			m.HelpScroll += delta
		}
		return m, nil
	}
	if m.Chat.Dialog != nil {
		if m.Chat.Dialog.Kind == "auth" && msg.Y < m.Height-1 {
			m.Chat.Dialog.Scroll += delta
		}
		return m, nil
	}
	if msg.Y >= len(m.tabChrome())+viewHeight(m) {
		return m, nil
	}
	switch m.Tabs[m.Active].at.view {
	case ViewJourney:
		if len(m.journeyChoices()) > 0 {
			m.moveJourney(delta)
		} else {
			m.Tabs[m.Active].at.journey.Scroll += delta
		}
	case ViewFiles:
		if m.Files.Viewer != nil {
			m.Files.Viewer.Scroll += delta
		} else {
			m.Files.Sel = min(max(0, m.Files.Sel+delta), max(0, len(m.Files.Files)-1))
		}
	case ViewDiff, ViewChanges:
		if m.Diff.Viewer != nil {
			m.diffScroll(delta, viewHeight(m))
		} else {
			m.diffMove(delta)
		}
	case ViewPlans:
		if m.Plans.Detail != nil {
			m.plansScroll(delta, viewHeight(m))
		} else {
			m.plansMove(delta)
		}
	case ViewSessions:
		if m.Sessions.Detail != nil {
			m.sessionsScroll(delta, viewHeight(m))
		} else {
			m.Sessions.Sel = min(max(0, m.Sessions.Sel+delta), max(0, len(m.Sessions.Rows)-1))
		}
	case ViewSettings:
		if m.Settings.Editing == nil {
			m.Settings.Sel = min(max(0, m.Settings.Sel+delta), len(settingFields())-1)
		}
	case ViewManage:
		m.Serve.Scroll += delta
	case ViewDecisions:
		m.Decisions.scroll += delta
	case ViewWorkflows:
		m.Wf.Sel = min(max(0, m.Wf.Sel+delta), max(0, len(m.Wf.Rows)-1))
	}
	return m, nil
}

func chatScrollTrack(total, page, top, height int) []string {
	track := make([]string, height)
	start, thumb := chatTrackMetrics(total, page, top, height)
	for i := range track {
		track[i] = FaintSt.Render("·")
		if i >= start && i < start+thumb {
			track[i] = InfoSt.Render("|") // ASCII keeps the thumb legible in terminal fonts.
		}
	}
	return track
}

func chatTrackMetrics(total, page, top, height int) (int, int) {
	thumb := min(height, max(1, height*page/max(1, total)))
	return min(max(0, height-thumb), max(0, top)*(height-thumb)/max(1, total-page)), thumb
}
func (m *Model) seekChatTrack(position int) {
	total := len(chatRows(m, chatTextWidth(m.Width)))
	page, height := chatBodyHeight(viewHeight(*m)), viewHeight(*m)
	_, thumb := chatTrackMetrics(total, page, 0, height)
	limit := max(0, total-page)
	m.setChatScrollTop(max(0, position)*limit/max(1, height-thumb), limit)
}
func (m *Model) setChatScrollTop(position, limit int) {
	m.Chat.pinTop = min(limit, max(0, position))
	m.Chat.pinned = m.Chat.pinTop < limit
	if m.Chat.pinned {
		m.captureChatAnchor()
	} else {
		m.Chat.unpin()
	}
}

func withChatScrollTrack(rows []string, w, total, page, top int) []string {
	track := chatScrollTrack(total, page, top, len(rows))
	for i, row := range rows {
		row = truncate(row, max(1, w-2))
		rows[i] = row + strings.Repeat(" ", max(0, w-2-ansi.StringWidth(row))) + " " + track[i]
	}
	return rows
}
