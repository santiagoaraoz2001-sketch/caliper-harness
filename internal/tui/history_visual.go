package tui

import (
	"fmt"
	"specific-harness/internal/watch"
	"strings"
)

func (m Model) historyListHeight(h int) int {
	budget := max(1, h-1)
	preview := 0
	if budget >= 6 {
		preview = min(5, budget-3)
	}
	return max(1, budget-preview)
}
func captureQuality(ev watch.Event) string {
	switch {
	case ev.ReadError != "":
		return "read failed · last known"
	case ev.Op == watch.OpDelete:
		return "deleted excerpt"
	case ev.Truncated:
		return "partial capture"
	case !ev.BeforeKnown:
		return "baseline unknown"
	case ev.BeforePartial:
		return "partial comparison"
	default:
		return "captured source"
	}
}
func (m *Model) historyLines(w, h int) []string {
	indices := m.diffIndices()
	if len(indices) == 0 {
		text := "No retained observed changes for this file in this harness session."
		if m.Diff.WatchErr != "" {
			text = "Watcher off: " + m.Diff.WatchErr
		}
		return []string{Guidance.Render(truncate(text, w))}
	}
	room := m.historyListHeight(h)
	selected := 0
	for i, idx := range indices {
		if idx == m.Diff.Sel {
			selected = i
		}
	}
	var list []string
	for _, idx := range indices {
		ev := m.Diff.Events[idx]
		counts := "baseline unknown"
		if ev.BeforeKnown {
			counts = fmt.Sprintf("+%d −%d", ev.AddedN, ev.RemovedN)
		}
		prefix := fmt.Sprintf("  #%d  %s  %s · %s · ", ev.Seq, ev.At.Format("15:04:05"), ev.Op, counts)
		identity := PathSt.Render(ev.Path)
		if m.Diff.Filter != "" {
			style := Guidance
			if ev.ReadError != "" {
				style = ErrSt.Bold(false)
			} else if ev.Truncated || !ev.BeforeKnown || ev.BeforePartial || ev.Op == watch.OpDelete {
				style = WarnSt.Bold(false)
			}
			identity = style.Render(captureQuality(ev))
		}
		row := Guidance.Render(prefix) + identity
		if idx == m.Diff.Sel {
			row = selectionRow("❯ "+strings.TrimPrefix(prefix, "  ")+identity, w)
		}
		list = append(list, truncate(row, w))
	}
	out := listWindow(list, selected, room)
	preview := max(0, h-1-room)
	if preview > 0 {
		ev := m.Diff.Events[indices[selected]]
		rows := []string{Guidance.Render(fmt.Sprintf("Preview #%d · %s", ev.Seq, captureQuality(ev)))}
		if ev.ReadError != "" {
			rows = append(rows, WarnSt.Bold(false).Render(truncate(ev.ReadError, w)))
		}
		type sourceRow struct {
			text  string
			added bool
		}
		var source []sourceRow
		if len(ev.Removed) > 0 {
			source = append(source, sourceRow{ev.Removed[0], false})
		}
		for _, line := range ev.Added[:min(2, len(ev.Added))] {
			source = append(source, sourceRow{line, true})
		}
		shown := min(len(source), max(0, preview-len(rows)-1))
		for _, line := range source[:shown] {
			prefix := "− "
			if line.added {
				prefix = "+ "
			}
			rows = append(rows, diffSource(prefix, line.text, w, line.added))
		}
		rest := len(ev.Removed) + len(ev.Added) - shown
		if ev.BeforeKnown {
			rest = max(rest, ev.RemovedN+ev.AddedN-shown)
		}
		tail := "Enter opens recorded change · /open-diff captured file"
		if rest > 0 {
			tail = fmt.Sprintf("… %d changed lines outside preview · enter opens", rest)
		}
		rows = append(rows, Guidance.Render(truncate(tail, w)))
		if len(out) < room {
			out = append(out, "")
		}
		out = append(out, rows...)
	}
	return fitRows(out, max(0, h-1))
}
