package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/config"
	"specific-harness/internal/watch"
)

// FileEntry is one path under the explorer root.
type FileEntry struct {
	Rel string
	Dir bool
}

// FilesState is the T6.9 explorer: a cwd-rooted recursive list plus
// one open file viewer sharing the FileView system.
type FilesState struct {
	Files      []FileEntry
	Sel        int
	Viewer     *FileView
	ShowHidden bool
	Truncated  bool
	Err        string
	Root, Path string
	Ticket     uint64
	Capture    *watch.Event
}

// filesLoadedMsg carries a finished list; filesOpenedMsg one file.
type filesLoadedMsg struct {
	files     []FileEntry
	truncated bool
	err       error
}

type filesOpenedMsg struct {
	root   string
	ticket uint64
	title  string
	lines  []string
	err    error
}

func (m *Model) filesActive() bool {
	return m.Tabs[m.Active].at.view == ViewFiles
}

// refreshFiles walks the cwd off the event loop, dirs first. Hidden
// paths hide unless toggled; the list caps with a flag, never silent.
func (m *Model) refreshFiles() tea.Cmd {
	root, err := os.Getwd()
	if err != nil {
		return func() tea.Msg { return filesLoadedMsg{err: err} }
	}
	showHidden, limit := m.Files.ShowHidden, m.Cfg.UI.MaxFileEntries
	return func() tea.Msg {
		files, truncated, err := listFiles(root, showHidden, limit)
		return filesLoadedMsg{files: files, truncated: truncated, err: err}
	}
}

// listFiles is the walk behind refreshFiles, rooted anywhere so
// tests drive it on temp dirs instead of the live cwd.
func listFiles(root string, showHidden bool, caps ...int) ([]FileEntry, bool, error) {
	limit := config.Defaults().UI.MaxFileEntries
	if len(caps) > 0 {
		limit = caps[0]
	}
	if limit <= 0 {
		return nil, false, fmt.Errorf("ui.max_file_entries must be positive")
	}
	var files []FileEntry
	truncated := false
	werr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		if !showHidden && isHiddenRel(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(files) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, FileEntry{Rel: rel})
		return nil
	})
	if werr != nil {
		return nil, false, werr
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, truncated, nil
}

// isHiddenRel reports dot-prefixed paths: .git, .env, a/.hidden.
func isHiddenRel(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// onFilesLoaded swaps in fresh entries, keeping the selection.
func (m *Model) onFilesLoaded(msg filesLoadedMsg) {
	if m.Files.Capture != nil {
		return
	} // An old explorer response cannot hide a retained snapshot.
	if msg.err != nil {
		m.Files.Err = truncate(msg.err.Error(), 120)
		return
	}
	if m.Files.Path == "" {
		m.Files.Err = ""
	}
	keep := ""
	if m.Files.Sel < len(m.Files.Files) {
		keep = m.Files.Files[m.Files.Sel].Rel
	}
	m.Files.Files, m.Files.Truncated = msg.files, msg.truncated
	m.Files.Sel = 0
	for i, f := range msg.files {
		if f.Rel == keep {
			m.Files.Sel = i
		}
	}
}

// openFile reads one entry off the event loop: capped lines, binary
// refuses loud with a one-liner.
func (m *Model) openFile(rel string) tea.Cmd {
	m.Files.Capture = nil
	root, err := os.Getwd()
	return m.readSelectedFile(root, rel, err)
}

func (m *Model) readSelectedFile(root, rel string, rootErr error) tea.Cmd {
	if m.Files.Root != root || m.Files.Path != rel {
		m.Files.Viewer = nil
		m.Files.Err = ""
	}
	m.Files.Root, m.Files.Path = root, rel
	m.Files.Ticket++
	ticket, cap, lineCap := m.Files.Ticket, m.Cfg.Tools.Bash.MaxBytes, m.Cfg.UI.MaxFileViewLines
	return func() tea.Msg {
		var lines []string
		err := rootErr
		if err == nil {
			lines, err = readFile(root, rel, cap, lineCap)
		}
		return filesOpenedMsg{root: root, title: rel, ticket: ticket, lines: lines, err: err}
	}
}

// readFile loads one root-relative file: binary refuses loud,
// long files cap with a flag.
func readFile(root, rel string, caps ...int) ([]string, error) {
	cap, lineCap := config.Defaults().Tools.Bash.MaxBytes, config.Defaults().UI.MaxFileViewLines
	if len(caps) > 1 {
		lineCap = caps[1]
	}
	if lineCap <= 0 {
		return nil, fmt.Errorf("ui.max_file_view_lines must be positive")
	}
	if len(caps) != 0 {
		cap = caps[0]
	}
	if cap <= 0 {
		return nil, fmt.Errorf("max_bytes must be positive")
	}
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(cap)))
	if err != nil {
		return nil, err
	}
	var probe [1]byte
	n, err := f.Read(probe[:])
	if err != nil && err != io.EOF {
		return nil, err
	}
	omitted := n != 0
	if omitted {
		for len(raw) > 0 {
			r, size := utf8.DecodeLastRune(raw)
			if r != utf8.RuneError || size != 1 {
				break
			}
			raw = raw[:len(raw)-1]
		}
	}
	for _, b := range raw[:min(512, len(raw))] {
		if b == 0 {
			return nil, fmt.Errorf("%s is binary — no text view", rel)
		}
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) > lineCap {
		lines = append(lines[:lineCap], "…more lines capped")
	}
	if omitted {
		lines = append(lines, "…bytes omitted at max_bytes; increase tools.bash.max_bytes to view more")
	}
	return lines, nil
}

// onFilesOpened lands the viewer, loud on failure.
func (m *Model) onFilesOpened(msg filesOpenedMsg) {
	if m.Files.Path == "" || msg.root != m.Files.Root || msg.title != m.Files.Path || msg.ticket != m.Files.Ticket {
		return
	}
	if msg.err != nil {
		m.Files.Viewer = nil
		m.Files.Err = msg.err.Error() + "; retrying each refresh"
		return
	}
	anchor := 1
	if m.Files.Viewer != nil {
		anchor = layoutLines(m.Files.Viewer.Logical, BoxInner(m.Width)).topLine(m.Files.Viewer.Scroll)
	}
	m.Files.Err = ""
	m.Files.Viewer = &FileView{Title: msg.title, Logical: msg.lines, Scroll: layoutLines(msg.lines, BoxInner(m.Width)).gotoLine(anchor, m.filesAvail(m.Width, viewHeight(*m)))}
}

// filesCur is the selected entry, or nil on an empty list.
func (m *Model) filesCur() *FileEntry {
	if m.Files.Sel < 0 || m.Files.Sel >= len(m.Files.Files) {
		return nil
	}
	return &m.Files.Files[m.Files.Sel]
}

// filesKey drives the explorer: arrows move or scroll, enter opens,
// h toggles hidden, esc walks back (handled globally).
func (m Model) filesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	vw := m.Files.Viewer != nil
	switch msg.Type {
	case tea.KeyUp:
		if vw {
			m.Files.Viewer.Scroll--
			if m.Files.Viewer.Scroll < 0 {
				m.Files.Viewer.Scroll = 0
			}
		} else if len(m.Files.Files) > 0 {
			m.Files.Sel = (m.Files.Sel - 1 + len(m.Files.Files)) % len(m.Files.Files)
		}
		return m, nil, true
	case tea.KeyDown:
		if vw {
			m.Files.Viewer.Scroll++
		} else if len(m.Files.Files) > 0 {
			m.Files.Sel = (m.Files.Sel + 1) % len(m.Files.Files)
		}
		return m, nil, true
	case tea.KeyPgUp:
		if vw {
			m.Files.Viewer.Scroll -= m.filesAvail(m.Width, viewHeight(m))
			if m.Files.Viewer.Scroll < 0 {
				m.Files.Viewer.Scroll = 0
			}
		}
		return m, nil, true
	case tea.KeyPgDown:
		if vw {
			m.Files.Viewer.Scroll += m.filesAvail(m.Width, viewHeight(m))
		}
		return m, nil, true
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		if !vw {
			if f := m.filesCur(); f != nil {
				cmd := m.openFile(f.Rel)
				return m, cmd, true
			}
		}
		return m, nil, true
	}
	switch msg.String() {
	case "h":
		if !vw && strings.TrimSpace(m.Input.Value()) == "" {
			m.Files.ShowHidden = !m.Files.ShowHidden
			return m, m.refreshFiles(), true
		}
	}
	return m, nil, false
}

func (m Model) renderFiles(w, vh int) string {
	inner := BoxInner(w)
	title := itemTitle("Files", m.Files.Sel, len(m.Files.Files))
	hints := m.filesHintRows(w, m.Files.Viewer != nil, m.Files.ShowHidden)
	room := max(1, vh-3-len(hints))
	var body []string
	if m.Files.Err != "" {
		for _, l := range Wrap(m.Files.Err, inner) {
			body = append(body, ErrSt.Render(l))
		}
	} else if m.Files.Viewer != nil {
		body = append(body, m.renderFileViewer(w, vh)...)
	} else {
		body = append(body, Muted.Render("Under the working dir"))
		body = append(body, listWindow(m.renderFileList(), m.Files.Sel, room)...)
	}
	if v := m.Files.Viewer; v != nil {
		layout := layoutLines(v.Logical, inner)
		avail := m.filesAvail(w, vh)
		top := layout.clamp(v.Scroll, avail)
		title = fmt.Sprintf("File · lines %d-%d/%d", layout.topLine(top), layout.topLine(top+max(0, avail-1)), len(v.Logical))
	}
	return panel(title, body, hints, w, vh)
}

func (m Model) renderFileList() []string {
	if len(m.Files.Files) == 0 {
		return []string{Muted.Render("no files — the working dir reads empty")}
	}
	var lines []string
	for i, f := range m.Files.Files {
		mark := "  "
		if i == m.Files.Sel {
			mark = "❯ "
		}
		line := mark + f.Rel
		if i == m.Files.Sel {
			lines = append(lines, selectionRow(line, BoxInner(m.Width)))
		} else {
			lines = append(lines, Body.Render(line))
		}
	}
	if m.Files.Truncated {
		lines = append(lines, Muted.Render(fmt.Sprintf("first %d shown — list capped", len(m.Files.Files))))
	}
	return lines
}

func (m Model) renderFileViewer(w, vh int) []string {
	vw := m.Files.Viewer
	colored := make([]string, len(vw.Logical))
	for i, line := range vw.Logical {
		colored[i] = sourceText(line)
	}
	body := Numbered(colored, BoxInner(w))
	avail := m.filesAvail(w, vh)
	scroll := layoutLines(vw.Logical, BoxInner(w)).clamp(vw.Scroll, avail)
	var lines []string
	for _, l := range Wrap(vw.Title, BoxInner(w)) {
		lines = append(lines, PathSt.Render(l))
	}
	if notice := m.captureNotice(); notice != "" {
		for _, l := range Wrap(notice, BoxInner(w)) {
			style := Guidance
			if m.Files.Capture.Truncated || m.Files.Capture.ReadError != "" || m.Files.Capture.Op == watch.OpDelete {
				style = WarnSt.Bold(false)
			}
			lines = append(lines, style.Render(l))
		}
	}
	for i := 0; i < avail; i++ {
		if scroll+i < len(body) {
			lines = append(lines, body[scroll+i])
		} else {
			lines = append(lines, "")
		}
	}
	return lines
}

func (m Model) filesHintRows(w int, viewer, hidden bool) []string {
	var rows []string
	for _, hint := range m.filesHints(viewer, hidden) {
		rows = append(rows, Wrap(hint, BoxInner(w))...)
	}
	return rows
}
func (m Model) filesAvail(w, h int) int {
	titleRows := 1
	if m.Files.Viewer != nil {
		titleRows = len(Wrap(m.Files.Viewer.Title, BoxInner(w)))
	}
	if notice := m.captureNotice(); notice != "" {
		titleRows += len(Wrap(notice, BoxInner(w)))
	}
	return max(0, h-2-titleRows-len(m.filesHintRows(w, true, false)))
}

func (m Model) filesHints(vw, hidden bool) []string {
	h := "↑↓ move · enter open · h hidden"
	if hidden {
		h = "↑↓ move · enter open · h shown"
	}
	if vw {
		h = "↑↓/pgup/pgdn scroll · /goto <line> · /comment [lines] text · /changes history"
	}
	return []string{Guidance.Render(m.ownedHint(h + " · " + m.escapeHint()))}
}
