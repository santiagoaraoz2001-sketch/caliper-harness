package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/config"
	"specific-harness/internal/watch"
)

// workStride is one event box (7 lines) plus its arrow line.
// workBoxDiff caps diff rows inside an event box.
const (
	workStride  = 8
	workBoxDiff = 3
)

// DiffState is the diff view: watcher event ring, selection, viewer.
type DiffState struct {
	Watcher    *watch.Watcher
	Root       string
	Events     []watch.Event
	Max        int
	AttrWindow time.Duration
	Sel        int
	Viewer     *DiffViewer
	WatchErr   string
	History    bool
	Filter     string // exact root-relative path; empty means all retained captures
}

// DiffViewer shows one event's file.
type DiffViewer struct {
	Ev     watch.Event
	Scroll int
}

// diffEventMsg carries one watcher event into the loop.
type diffEventMsg struct{ ev watch.Event }

// A burst is one transcript position. Individual captures and IDs remain in
// Diff.Events; no input/output/evidence maps are deleted or reindexed.
type observedChangeGroup struct {
	Count, First, Last int
	Preview            bool
}

func (m *Model) appendObservedChange(ev watch.Event) {
	if m.Chat.DiffGroups == nil {
		m.Chat.DiffGroups = map[int]observedChangeGroup{}
	}
	at := len(m.Chat.Lines) - 1
	boundary := len(m.Chat.Thoughts[len(m.Chat.Lines)]) > 0 || len(m.Chat.NativeRequests[len(m.Chat.Lines)]) > 0
	reading := m.Chat.pinned && m.Chat.readOrigin.Valid && m.Chat.readOrigin.Line == at
	if at >= 0 && m.Chat.Lines[at].Kind == lineDiff && !boundary && !reading {
		if group, ok := m.Chat.DiffGroups[at]; ok {
			group.Count++
			group.Last = ev.Seq
			m.Chat.DiffGroups[at] = group
			m.Chat.Lines[at].Text = strconv.Itoa(ev.Seq)
			return
		}
	}
	m.Chat.appendLine(lineDiff, strconv.Itoa(ev.Seq))
	m.Chat.DiffGroups[len(m.Chat.Lines)-1] = observedChangeGroup{Count: 1, First: ev.Seq, Last: ev.Seq, Preview: m.Chat.Running}
}

// newDiffState starts the cwd watcher unless config disables it.
func newDiffState(cfg config.Config) DiffState {
	st := DiffState{Sel: -1, Max: cfg.Watch.MaxEvents, AttrWindow: time.Duration(cfg.Watch.AttrWindowS) * time.Second}
	if st.Max <= 0 {
		st.Max = 100
	}
	if !cfg.Watch.Enabled {
		return st
	}
	root, err := os.Getwd()
	if err != nil {
		st.WatchErr = err.Error()
		return st
	}
	st.Root = root
	wcfg := watch.DefaultConfig()
	wcfg.MaxBytes, wcfg.StartupMaxBytes, wcfg.StartupMaxFiles = cfg.Watch.MaxFileBytes, cfg.Watch.StartupMaxBytes, cfg.Watch.StartupMaxFiles
	wcfg.StartupMaxEntries = cfg.Watch.StartupMaxEntries
	if ms := cfg.Watch.DebounceMs; ms > 0 {
		wcfg.Debounce = time.Duration(ms) * time.Millisecond
	}
	if cfg.Watch.DiffLines > 0 {
		wcfg.DiffLines = cfg.Watch.DiffLines
	}
	if cfg.Watch.FileLines > 0 {
		wcfg.FileLines = cfg.Watch.FileLines
	}
	wcfg.Ignore = append(wcfg.Ignore, cfg.Watch.Ignore...)
	for _, p := range []string{cfg.Sessions.Path, cfg.Store.Path} {
		if p == "" {
			continue
		}
		rel := p
		if filepath.IsAbs(p) {
			var err error
			rel, err = filepath.Rel(root, p)
			if err != nil {
				continue
			}
		}
		rel = filepath.ToSlash(rel)
		if rel != ".." && !strings.HasPrefix(rel, "../") {
			wcfg.Ignore = append(wcfg.Ignore, rel)
		}
	}
	w, err := watch.NewAsync(root, wcfg)
	if err != nil {
		st.WatchErr = err.Error()
		return st
	}
	st.Watcher = w
	return st
}

// watchFeed forwards watcher events into the loop.
func (m Model) watchFeed() tea.Cmd {
	return func() tea.Msg {
		w := m.Diff.Watcher
		if w == nil {
			return nil
		}
		go func() {
			for ev := range w.Events() {
				if m.Shared.Prog != nil {
					m.Shared.Prog.Send(diffEventMsg{ev: ev})
				}
			}
		}()
		return nil
	}
}

func (m *Model) diffActive() bool {
	v := m.Tabs[m.Active].at.view
	return v == ViewDiff || v == ViewChanges
}

// History scope belongs to the navigable route, including tab/back/forward.
func (m *Model) syncDiffScope() {
	if !m.diffActive() {
		return
	}
	at := m.Tabs[m.Active].at
	history := at.view == ViewChanges
	filter := ""
	if history {
		filter = at.target
	}
	changed := history != m.Diff.History || filter != m.Diff.Filter
	m.Diff.History, m.Diff.Filter = history, filter
	if changed {
		m.Diff.Viewer = nil
		m.Diff.Sel = -1
		if indices := m.diffIndices(); len(indices) > 0 {
			m.Diff.Sel = indices[len(indices)-1]
		}
	}
}

// onDiffEvent stamps attribution, appends to the ring, and follows
// the latest unless the user scrolled up.
func (m *Model) onDiffEvent(msg diffEventMsg) {
	ev := msg.ev
	filteredFollow := false
	if m.Diff.Filter != "" {
		indices := m.diffIndices()
		filteredFollow = len(indices) == 0 || m.Diff.Sel == indices[len(indices)-1]
	}
	if m.lastTool != "" && time.Since(m.lastToolAt) < m.Diff.AttrWindow {
		ev.AfterTool = m.lastTool
	}
	if m.Chat.Running || m.Tabs[m.Active].at.view == ViewChat {
		m.appendObservedChange(ev)
	}
	m.Diff.Events = append(m.Diff.Events, ev)
	if max := m.Diff.Max; max > 0 && len(m.Diff.Events) > max {
		drop := len(m.Diff.Events) - max
		m.Diff.Events = m.Diff.Events[drop:]
		m.Diff.Sel -= drop
	}
	if m.Diff.Filter != "" {
		indices := m.diffIndices()
		if len(indices) == 0 {
			m.Diff.Sel = -1
		} else if filteredFollow && ev.Path == m.Diff.Filter {
			m.Diff.Sel = indices[len(indices)-1]
		} else {
			found := false
			for _, idx := range indices {
				if idx == m.Diff.Sel {
					found = true
				}
			}
			if !found {
				m.Diff.Sel = indices[0]
			}
		}
	} else if n := len(m.Diff.Events); m.Diff.Sel >= n-2 {
		m.Diff.Sel = n - 1
	}
}

func (m *Model) diffCur() *watch.Event {
	if m.Diff.Sel < 0 || m.Diff.Sel >= len(m.Diff.Events) {
		return nil
	}
	return &m.Diff.Events[m.Diff.Sel]
}

func (m *Model) diffIndices() []int {
	var indices []int
	for i, ev := range m.Diff.Events {
		if m.Diff.Filter == "" || ev.Path == m.Diff.Filter {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) diffMove(d int) {
	indices := m.diffIndices()
	if len(indices) == 0 {
		return
	}
	pos := 0
	for i, idx := range indices {
		if idx == m.Diff.Sel {
			pos = i
			break
		}
	}
	m.Diff.Sel = indices[min(max(0, pos+d), len(indices)-1)]
}

// viewerAvail is the file-body window for height h.
func viewerAvail(ev watch.Event, h int) int {
	budget := max(0, h-1)
	header := min(1+len(ev.Removed)+len(ev.Added), max(0, budget-2))
	return budget - header - min(1, max(0, budget-1))
}

func viewerBody(ev watch.Event) []string {
	if len(ev.After) > 0 {
		return ev.After
	}
	if len(ev.Removed) > 0 {
		return append([]string{"(deleted — last seen content)"}, ev.Removed...)
	}
	return []string{"(no content captured)"}
}

func (m *Model) diffScroll(d, h int) {
	h = m.diffHeight(h, m.Width)
	vw := m.Diff.Viewer
	if vw == nil {
		return
	}
	vw.Scroll = layoutLines(viewerBody(vw.Ev), BoxInner(m.Width)).clamp(vw.Scroll+d, viewerAvail(vw.Ev, h))
}

// diffComment jumps to chat with /comment prefilled for the event.
func (m *Model) diffComment(ev *watch.Event) {
	if ev == nil {
		return
	}
	m.commentPrefill(ev.Path)
}

// diffKey drives diff-view nav. done=false falls through to input.
func (m Model) diffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	vw := m.Diff.Viewer != nil
	switch msg.Type {
	case tea.KeyUp:
		if vw {
			m.diffScroll(-1, viewHeight(m))
		} else {
			m.diffMove(-1)
		}
		return m, nil, true
	case tea.KeyDown:
		if vw {
			m.diffScroll(1, viewHeight(m))
		} else {
			m.diffMove(1)
		}
		return m, nil, true
	case tea.KeyPgUp:
		if vw {
			m.diffScroll(-viewerAvail(m.Diff.Viewer.Ev, m.diffHeight(viewHeight(m), m.Width)), viewHeight(m))
		} else if m.Diff.History {
			m.diffMove(-max(1, m.historyListHeight(m.diffHeight(viewHeight(m), m.Width))))
		}
		return m, nil, true
	case tea.KeyPgDown:
		if vw {
			m.diffScroll(viewerAvail(m.Diff.Viewer.Ev, m.diffHeight(viewHeight(m), m.Width)), viewHeight(m))
		} else if m.Diff.History {
			m.diffMove(max(1, m.historyListHeight(m.diffHeight(viewHeight(m), m.Width))))
		}
		return m, nil, true
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		if vw {
			return m, nil, true
		}
		if ev := m.diffCur(); ev != nil {
			m.Diff.Viewer = &DiffViewer{Ev: *ev}
		}
		return m, nil, true
	case tea.KeyRunes:
		s := msg.String()
		if (s == "f" || s == "F") && strings.TrimSpace(m.Input.Value()) == "" {
			if vw {
				m.diffComment(&m.Diff.Viewer.Ev)
			} else {
				m.diffComment(m.diffCur())
			}
			return m, nil, true
		}
		return m, nil, false
	}
	return m, nil, false
}

func diffTitle(sel bool, ev watch.Event) string {
	mark := "  "
	if sel {
		mark = "❯ "
	}
	return fmt.Sprintf("%s#%d %s · %s", mark, ev.Seq, ev.Path, ev.Op)
}

func diffMeta(ev watch.Event) string {
	meta := ev.At.Format("15:04:05")
	if !ev.BeforeKnown {
		meta += " · baseline unknown"
	} else if ev.BeforePartial || ev.Truncated {
		meta += " · partial observed comparison"
	}
	if ev.ReadError != "" {
		meta += " · read failed; last capture: " + ev.ReadError
	}
	if ev.AfterTool != "" {
		meta += " · after " + truncate(ev.AfterTool, 40)
	}
	return meta
}

// diffBox renders one event as a fixed 7-line box.
func diffBox(ev watch.Event, sel bool, w int) string {
	inner := BoxInner(w)
	lines := []string{truncate(diffMeta(ev), inner)}
	shown := 0
	for _, l := range ev.Removed {
		if shown >= workBoxDiff {
			break
		}
		lines = append(lines, diffSource("- ", l, inner, false))
		shown++
	}
	for _, l := range ev.Added {
		if shown >= workBoxDiff {
			break
		}
		lines = append(lines, diffSource("+ ", l, inner, true))
		shown++
	}
	for len(lines) < 1+workBoxDiff {
		lines = append(lines, "")
	}
	summary := ""
	if !ev.BeforeKnown {
		summary = "baseline unknown; open observed content"
	}
	if rest := ev.AddedN + ev.RemovedN - shown; ev.BeforeKnown && rest > 0 {
		summary = fmt.Sprintf("…%d more", rest)
	} else if ev.Truncated {
		summary = "…capped"
	}
	lines = append(lines, summary)
	return Box(diffTitle(sel, ev), strings.Join(lines, "\n"), w)
}

// diffCoverage is shared by rendering and logical viewer geometry.
func (m *Model) diffCoverage(w int) []string {
	if m.Diff.Watcher == nil {
		return nil
	}
	s := m.Diff.Watcher.Stats()
	rows := Wrap(fmt.Sprintf("Startup %d files/%d bytes · partial %d · omitted %d · skipped %d · dropped %d · watch errors %d", s.Files, s.Bytes, s.Partial, s.Omitted, s.Skipped, s.Dropped, s.Errors), w)
	if s.Initializing {
		rows = append(Wrap("Scanning workspace in background · change coverage is incomplete", w), rows...)
	}
	if s.Limited {
		rows = append(rows, Wrap("Workspace scan limit reached · unvisited subdirectories are not watched; open a narrower project directory or increase watch.startup_max_entries", w)...)
	}
	if s.Error != "" {
		rows = append(rows, Wrap("error: "+strings.ReplaceAll(s.Error, "\n", " "), w)...)
	}
	return rows
}

func (m *Model) diffHeight(h, w int) int {
	return h - len(m.diffCoverage(w)) - len(m.diffHintRows(w)) - len(m.historyNotice(w))
}

func (m Model) historyNotice(w int) []string {
	if !m.Diff.History {
		return nil
	}
	return Wrap(fmt.Sprintf("Recent observed changes · this harness session · up to %d captures across workspace · no Git history", m.Diff.Max), w)
}

func (m Model) diffHintRows(w int) []string {
	text := "↑↓ select · enter open · f comment"
	if m.Diff.History {
		text = "↑↓ select · pgup/pgdn pages · enter open · f comment"
	}
	if m.Diff.Viewer != nil {
		text = "↑↓/pgup/pgdn scroll · f comment"
	}
	text += " · /open-diff [event] file viewer"
	text = m.ownedHint(text + " · " + m.escapeHint())
	var out []string
	for _, l := range Wrap(text, max(1, w-4)) {
		out = append(out, hintRule(l, w))
	}
	return out
}

// renderDiff draws header plus the event workflow in exactly h rows.
func (m *Model) renderDiff(w, h int) string {
	left := fmt.Sprintf("Diff · %d events", len(m.Diff.Events))
	if m.Diff.History {
		indices := m.diffIndices()
		pos := 0
		for i, idx := range indices {
			if idx == m.Diff.Sel {
				pos = i + 1
			}
		}
		left = fmt.Sprintf("Changes · %d/%d captures", pos, len(indices))
		if m.Diff.Filter != "" {
			left += " · " + m.Diff.Filter
		}
	}
	rows := []string{Title.Render(truncate(left, w))}
	rows = append(rows, m.diffHintRows(w)...)
	rows = append(rows, m.diffCoverage(w)...)
	for _, line := range m.historyNotice(w) {
		rows = append(rows, Guidance.Render(line))
	}
	contentHeight := m.diffHeight(h, w)
	if m.Diff.Viewer != nil {
		rows = append(rows, m.viewerLines(w, contentHeight)...)
	} else {
		rows = append(rows, m.eventLines(w, contentHeight)...)
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return strings.Join(rows[:h], "\n")
}

func (m *Model) eventLines(w, h int) []string {
	if m.Diff.History {
		return m.historyLines(w, h)
	}
	budget := h - 1
	if budget < 1 {
		return nil
	}
	indices := m.diffIndices()
	if len(indices) == 0 {
		msg := "No file events yet — edits under " + m.Diff.Root + " appear here."
		if m.Diff.History {
			msg = "No retained observed changes for this file in this harness session."
		}
		if m.Diff.WatchErr != "" {
			msg = "Watcher off: " + m.Diff.WatchErr
		}
		return []string{Muted.Render(truncate(msg, w))}
	}
	n := budget / workStride
	if n < 1 {
		n = 1
	}
	selected := 0
	for i, idx := range indices {
		if idx == m.Diff.Sel {
			selected = i
		}
	}
	start := selected - n + 1
	if start < 0 {
		start = 0
	}
	end := start + n
	if end > len(indices) {
		end = len(indices)
	}
	var rows []string

	for i := start; i < end; i++ {
		idx := indices[i]
		rows = append(rows, strings.Split(diffBox(m.Diff.Events[idx], idx == m.Diff.Sel, w), "\n")...)
		if i < end-1 {
			rows = append(rows, FaintSt.Render("  ↓"))
		}
	}
	if end < len(indices) {
		rows = append(rows, Guidance.Render(fmt.Sprintf("…%d later", len(indices)-end)))
	}
	if len(rows) > budget {
		rows = rows[:budget]
	}
	return rows
}

func (m *Model) viewerLines(w, h int) []string {
	budget := h - 1
	if budget < 1 {
		return nil
	}
	inner := BoxInner(w)
	ev := m.Diff.Viewer.Ev
	var rows []string
	rows = append(rows, truncate(diffMeta(ev), inner))
	for _, l := range ev.Removed {
		rows = append(rows, diffSource("- ", l, inner, false))
	}
	for _, l := range ev.Added {
		rows = append(rows, diffSource("+ ", l, inner, true))
	}
	limit := max(0, budget-2)
	if len(rows) > limit {
		omitted := len(rows) - max(0, limit-1)
		rows = rows[:max(0, limit-1)]
		if limit > 0 {
			rows = append(rows, Muted.Render(fmt.Sprintf("…%d summary rows omitted", omitted)))
		}
	}
	if budget > 1 {
		rows = append(rows, FaintSt.Render("── file ──"))
	}
	avail := viewerAvail(ev, h)
	// Numbered like every viewer: /goto and /comment lines land here.
	logical := viewerBody(ev)
	colored := make([]string, len(logical))
	for i, line := range logical {
		colored[i] = sourceText(line)
	}
	body := Numbered(colored, inner)
	scroll := layoutLines(viewerBody(ev), inner).clamp(m.Diff.Viewer.Scroll, avail)
	for i := 0; i < avail; i++ {
		if scroll+i < len(body) {
			rows = append(rows, body[scroll+i])
		} else {
			rows = append(rows, "")
		}
	}
	if len(rows) > budget {
		rows = rows[:budget]
	}
	return rows
}
