package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// changesChat projects the existing watcher ring; it does not create a store.
func (m *Model) changesChat(args []string) {
	path := strings.Join(args, " ")
	if path == "" {
		if m.filesActive() && m.Files.Viewer != nil {
			path = m.Files.Path
		} else if m.diffActive() && m.Diff.Viewer != nil {
			path = m.Diff.Viewer.Ev.Path
		}
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		path = ""
	}
	// Switch first: Diff and Changes share the watcher, but each tab keeps its
	// own viewer bookmark. Save the outgoing viewer before changing its scope.
	m.navigate(ViewChanges, path)
	if m.Tabs[m.Active].at.view != ViewChanges {
		return // An owned dialog may prevent navigation.
	}
	// An empty /changes scope is explicit, unlike bare inspector refocusing.
	m.Tabs[m.Active].at.target = path
	m.Diff.History, m.Diff.Filter, m.Diff.Viewer = true, path, nil
	m.Diff.Sel = -1
	if indices := m.diffIndices(); len(indices) > 0 {
		m.Diff.Sel = indices[len(indices)-1]
	}
}

// openDiffChat opens retained source, never the current working file.
func (m *Model) openDiffChat(args []string) {
	evIndex := -1
	if len(args) > 1 {
		m.alert("err", "use /open-diff [event]")
		return
	}
	if len(args) == 1 {
		seq, err := strconv.Atoi(args[0])
		if err != nil {
			m.alert("err", "event must be its numeric ID: /open-diff 12")
			return
		}
		for i, ev := range m.Diff.Events {
			if ev.Seq == seq {
				evIndex = i
				break
			}
		}
	} else if m.diffActive() {
		if m.Diff.Viewer != nil {
			for i, ev := range m.Diff.Events {
				if ev.Seq == m.Diff.Viewer.Ev.Seq {
					evIndex = i
					break
				}
			}
		} else {
			evIndex = m.Diff.Sel
		}
	} else if len(m.Diff.Events) > 0 {
		evIndex = len(m.Diff.Events) - 1
	}
	if evIndex < 0 || evIndex >= len(m.Diff.Events) {
		m.alert("err", "capture unavailable or expired · /diff lists retained events")
		return
	}
	ev := m.Diff.Events[evIndex]
	if len(ev.After) == 0 && len(ev.Removed) == 0 {
		m.alert("err", "no source content retained for this capture · /diff shows its metadata")
		return
	}
	m.Files.Ticket++ // Reject in-flight live-file responses.
	m.Files.Root, m.Files.Path, m.Files.Err = m.Diff.Root, ev.Path, ""
	m.Files.Capture = &ev
	m.Files.Viewer = &FileView{Title: ev.Path, Logical: append([]string(nil), viewerBody(ev)...)}
	m.navigate(ViewFiles, ev.Path)
	m.alert("info", m.captureNotice())
}

func (m Model) captureNotice() string {
	if ev := m.Files.Capture; ev != nil {
		status := "captured file"
		if ev.Op == "delete" {
			status = fmt.Sprintf("deleted · retained %d of at least %d prior lines", len(viewerBody(*ev)), ev.RemovedN)
		} else if ev.ReadError != "" {
			status = "read failed; last known capture · " + ev.ReadError
		} else if ev.Truncated {
			status = "partial captured file"
		}
		return fmt.Sprintf("Observed #%d · %s · %s · /changes history", ev.Seq, ev.At.Format("2006-01-02T15:04:05Z07:00"), status)
	}
	if m.Files.Viewer != nil {
		return m.Files.Viewer.Notice
	}
	return ""
}

// Capture identity travels with the exact quote; no lookup of current disk content.
func (m Model) activeCaptureNotice() string {
	if m.Tabs[m.Active].at.view == ViewFiles {
		return m.captureNotice()
	}
	if (m.Tabs[m.Active].at.view == ViewDiff || m.Tabs[m.Active].at.view == ViewChanges) && m.Diff.Viewer != nil {
		copy := m
		copy.Files.Capture = &m.Diff.Viewer.Ev
		return copy.captureNotice()
	}
	return ""
}
