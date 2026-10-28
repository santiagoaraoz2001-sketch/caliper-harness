package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// SessionRow is one past run: id, first prompt, event count.
type SessionRow struct {
	ID     string
	When   time.Time
	Prompt string
	Count  int
}

// SessionDetail is one open event stream with its scroll offset.
type SessionDetail struct {
	ID     string
	Events []session.Event
	Scroll int
}

// SessionsState is the browser: newest-first rows, selection, viewer.
type SessionsState struct {
	Rows    []SessionRow
	Skipped int
	Sel     int
	Detail  *SessionDetail
}

// sessionsLoadedMsg carries a finished list; sessionsOpenedMsg one
// finished open (err loud, never silent).
type sessionsLoadedMsg struct {
	rows    []SessionRow
	skipped int
	err     error
}

type sessionsOpenedMsg struct {
	id     string
	events []session.Event
	err    error
}

func (m *Model) sessionsActive() bool {
	return m.Tabs[m.Active].at.view == ViewSessions
}

// refreshSessions lists and summarizes every session off the event
// loop, newest first. Unreadable files skip with a count.
func (m *Model) refreshSessions() tea.Cmd {
	dir := m.SessionsPath
	return func() tea.Msg {
		ids, err := session.List(dir)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		var rows []SessionRow
		skipped := 0
		for _, id := range ids {
			events, err := session.Read(dir, id)
			if err != nil || len(events) == 0 {
				skipped++
				continue
			}
			prompt, when := sessionsPrompt(events)
			rows = append(rows, SessionRow{ID: id, When: when, Prompt: prompt, Count: len(events)})
		}
		return sessionsLoadedMsg{rows: rows, skipped: skipped}
	}
}

// sessionsPrompt summarizes a stream: its first prompt, else its
// first event. Time falls back to zero, rendered as —.
func sessionsPrompt(events []session.Event) (string, time.Time) {
	for _, e := range events {
		if e.Kind == "prompt" {
			return e.Text, e.Time
		}
	}
	return events[0].Text, events[0].Time
}

// historyFromEvents is retained for legacy callers; restoration owns all evidence.
func historyFromEvents(events []session.Event) []llm.Message {
	c, _, _ := session.Restore(events, 0)
	return c.Messages()
}
func BaselineContinuation(dir, id string, window int, modelKey string, catalogs ...*prompts.Catalog) (session.Continuation, string, error) {
	var pc *prompts.Catalog
	var err error
	if len(catalogs) > 0 {
		pc = catalogs[0]
	}
	if pc == nil {
		pc, err = prompts.Default()
		if err != nil {
			return session.Continuation{}, "", err
		}
	}
	events, err := session.ReadUsed(dir, id)
	if err != nil {
		return session.Continuation{}, "", err
	}
	return session.RestoreWithPrompts(events, window, pc, modelKey)
}

// AddSysLine appends a system line: startup notices like resume.
func (m *Model) AddSysLine(text string) { m.Chat.appendLine(lineSys, text) }

// BaselineHistory loads one session's turns for a fresh process: a
// new harness iteration starts its own session with these as context.
func BaselineHistory(sessionsDir, id string) ([]llm.Message, error) {
	events, err := session.ReadUsed(sessionsDir, id)
	if err != nil {
		return nil, fmt.Errorf("cannot resume %s: %v", id, err)
	}
	if history := historyFromEvents(events); len(history) > 0 {
		return history, nil
	}
	return nil, fmt.Errorf("cannot resume %s: no turns in it", id)
}

// resumeSession reopens the viewed stream in a new terminal tab: a
// fresh harness with the stream as baseline history. The viewer is
// the confirmation (what you saw is what resumes), and the current
// chat is untouched — mid-run included.
func (m *Model) resumeSession() {
	d := m.Sessions.Detail
	if d == nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		m.alert("err", fmt.Sprintf("cannot resume: %v", err))
		return
	}
	argv := []string{exe, "tui"}
	// Explicit project choices stay explicit. An automatic workspace path must
	// be rediscovered normally, or --config would override personal defaults.
	if m.CfgPath != "" && (m.Cfg.ProjectOverride || !config.ManagedWorkspaceConfig(m.CfgPath)) {
		argv = append(argv, "--config", m.CfgPath)
	}
	argv = append(argv, "--resume", d.ID)
	if err := terminalOpen(argv); err != nil {
		m.alert("err", err.Error())
		return
	}
	m.alert("info", fmt.Sprintf("resumed %s in a new terminal", d.ID))
}

// onSessionsLoaded swaps in fresh rows, keeping the selection on its id.
func (m *Model) onSessionsLoaded(msg sessionsLoadedMsg) {
	if msg.err != nil {
		m.alert("err", msg.err.Error())
		return
	}
	keep := ""
	if m.Sessions.Sel < len(m.Sessions.Rows) {
		keep = m.Sessions.Rows[m.Sessions.Sel].ID
	}
	m.Sessions.Rows, m.Sessions.Skipped = msg.rows, msg.skipped
	m.Sessions.Sel = 0
	for i, r := range msg.rows {
		if r.ID == keep {
			m.Sessions.Sel = i
		}
	}
}

// openSession reads one stream off the event loop.
func (m *Model) openSession(id string) tea.Cmd {
	dir := m.SessionsPath
	return func() tea.Msg {
		events, err := session.ReadUsed(dir, id)
		return sessionsOpenedMsg{id: id, events: events, err: err}
	}
}

// onSessionsOpened lands the viewer, loud on failure.
func (m *Model) onSessionsOpened(msg sessionsOpenedMsg) {
	if msg.err != nil {
		m.alert("err", fmt.Sprintf("cannot open session %s: %v", msg.id, msg.err))
		return
	}
	m.Sessions.Detail = &SessionDetail{ID: msg.id, Events: msg.events}
	m.alert("info", "opened session "+msg.id)
}

// sessionsCur is the selected row, or nil on an empty list.
func (m *Model) sessionsCur() *SessionRow {
	if m.Sessions.Sel < 0 || m.Sessions.Sel >= len(m.Sessions.Rows) {
		return nil
	}
	return &m.Sessions.Rows[m.Sessions.Sel]
}

// sessionsScroll clamps the viewer offset to its wrapped body.
func (m *Model) sessionsScroll(d, h int) {
	if m.Sessions.Detail == nil {
		return
	}
	body := sessionDisplay(m.Sessions.Detail, BoxInner(m.Width))
	avail := m.sessionBodyHeight(h)
	if avail < 1 {
		avail = 1
	}
	max := len(body) - avail
	if max < 0 {
		max = 0
	}
	m.Sessions.Detail.Scroll += d
	if m.Sessions.Detail.Scroll < 0 {
		m.Sessions.Detail.Scroll = 0
	}
	if m.Sessions.Detail.Scroll > max {
		m.Sessions.Detail.Scroll = max
	}
}

// sessionsKey drives the browser: arrows move or scroll, enter
// opens, r refreshes, esc walks back (handled globally).
func (m Model) sessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	vw := m.Sessions.Detail != nil
	switch msg.Type {
	case tea.KeyUp:
		if vw {
			m.sessionsScroll(-1, viewHeight(m))
		} else if len(m.Sessions.Rows) > 0 {
			m.Sessions.Sel = (m.Sessions.Sel - 1 + len(m.Sessions.Rows)) % len(m.Sessions.Rows)
		}
		return m, nil, true
	case tea.KeyDown:
		if vw {
			m.sessionsScroll(1, viewHeight(m))
		} else if len(m.Sessions.Rows) > 0 {
			m.Sessions.Sel = (m.Sessions.Sel + 1) % len(m.Sessions.Rows)
		}
		return m, nil, true
	case tea.KeyPgUp:
		if vw {
			m.sessionsScroll(-m.sessionBodyHeight(viewHeight(m)), viewHeight(m))
		}
		return m, nil, true
	case tea.KeyPgDown:
		if vw {
			m.sessionsScroll(m.sessionBodyHeight(viewHeight(m)), viewHeight(m))
		}
		return m, nil, true
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		if vw {
			m.resumeSession()
			return m, nil, true
		}
		if r := m.sessionsCur(); r != nil {
			return m, m.openSession(r.ID), true
		}
		return m, nil, true
	case tea.KeyRunes:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		if s := msg.String(); s == "r" || s == "R" {
			return m, m.refreshSessions(), true
		}
		return m, nil, false
	}
	return m, nil, false
}

// sessionDisplay renders one stream as wrapped time/kind/text rows.
func (m Model) sessionBodyHeight(h int) int {
	return max(1, h-4-len(m.sessionHints(true, BoxInner(m.Width))))
}

func sessionDisplay(d *SessionDetail, inner int) []string {
	out := []string{SectionSt.Render("Recorded context · inspection")}
	intent := "not recorded"
	for _, e := range d.Events {
		if e.Kind == "prompt" {
			intent = oneLine(e.Text)
			break
		}
	}
	for _, l := range Wrap("Intent: "+intent, inner) {
		out = append(out, Body.Render(l))
	}
	if len(d.Events) > 0 {
		first, last := d.Events[0], d.Events[len(d.Events)-1]
		when := "time unavailable"
		if !first.Time.IsZero() && !last.Time.IsZero() {
			when = first.Time.Format("01-02 15:04") + " → " + last.Time.Format("01-02 15:04")
		}
		for _, l := range Wrap(when+" · last recorded: "+last.Kind, inner) {
			out = append(out, Guidance.Render(l))
		}
	}
	out = append(out, "")
	return append(out, recordedEventRows(d.Events, inner)...)
}

func recordedEventRows(events []session.Event, inner int) []string {
	var out []string
	for _, e := range events {
		when := "—"
		if !e.Time.IsZero() {
			when = e.Time.Format("15:04:05")
		}
		style := InfoSt
		switch e.Kind {
		case "prompt":
			style = UserSt.Background(lipgloss.NoColor{})
		case "failure", "error", "failed":
			style = ErrSt.Bold(false)
		case "stopped", "cancelled":
			style = WarnSt.Bold(false)
		case "tool":
			style = PathSt
		}
		for _, l := range Wrap(when+" ["+e.Kind+"]", inner) {
			out = append(out, style.Render(l))
		}
		for _, l := range Wrap(e.Text, inner) {
			out = append(out, Body.Render(l))
		}
	}
	return out
}

// sessionHints is the full key list, wrapped to fit any frame.
func (m Model) sessionHints(vw bool, inner int) []string {
	h := "↑↓ select · enter open · r refresh"
	if vw {
		h = "↑↓/pgup/pgdn scroll · enter resume in new terminal"
	}
	var out []string
	for _, l := range Wrap(m.ownedHint(h+" · "+m.escapeHint()), inner) {
		out = append(out, Guidance.Render(l))
	}
	return out
}

// renderSessions draws the list or the open stream with its hints.
func (m *Model) renderSessions(w, vh int) string {
	inner := BoxInner(w)
	vw := m.Sessions.Detail != nil
	hints := m.sessionHints(vw, inner)
	room := max(1, vh-3-len(hints))
	body := []string{Muted.Render("Past runs · select to inspect recorded events")}
	if vw {
		body = append(body, m.renderSessionViewer(vh)...)
	} else {
		body = append(body, listWindow(m.renderSessionList(), m.Sessions.Sel, room)...)
	}
	return panel(itemTitle("Sessions", m.Sessions.Sel, len(m.Sessions.Rows)), body, hints, w, vh)
}

func (m Model) renderSessionList() []string {
	if len(m.Sessions.Rows) == 0 {
		return []string{Muted.Render("no sessions yet — runs land here")}
	}
	var lines []string
	for i, r := range m.Sessions.Rows {
		mark := "  "
		if i == m.Sessions.Sel {
			mark = "❯ "
		}
		when := "—"
		if !r.When.IsZero() {
			when = r.When.Format("01-02 15:04")
		}
		meta := fmt.Sprintf("%s · %s · %d events", r.ID, when, r.Count)
		line := PadRight(mark+truncate(oneLine(r.Prompt), max(8, BoxInner(m.Width)-len(meta)-4)), Guidance.Render(meta), BoxInner(m.Width))
		if i == m.Sessions.Sel {
			lines = append(lines, selectionRow(line, BoxInner(m.Width)))
		} else {
			lines = append(lines, Body.Render(line))
		}
	}
	if m.Sessions.Skipped > 0 {
		lines = append(lines, Muted.Render(fmt.Sprintf("%d unreadable — see sessions <id>", m.Sessions.Skipped)))
	}
	return lines
}

func (m Model) renderSessionViewer(vh int) []string {
	d := m.Sessions.Detail
	head := fmt.Sprintf("%s · %d events", d.ID, len(d.Events))
	avail := m.sessionBodyHeight(vh)
	if avail < 1 {
		avail = 1
	}
	body := sessionDisplay(d, BoxInner(m.Width))
	scroll := d.Scroll
	if scroll > len(body)-avail {
		scroll = len(body) - avail
	}
	if scroll < 0 {
		scroll = 0
	}
	out := []string{Title.Render(truncate(head, BoxInner(m.Width)))}
	if scroll+avail > len(body) {
		avail = len(body) - scroll
	}
	for _, l := range body[scroll : scroll+avail] {
		out = append(out, l)
	}
	return out
}

// oneLine flattens a prompt to its first line for list rows.
func oneLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
