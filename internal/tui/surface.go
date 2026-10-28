package tui

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// surface contains display data only. The existing Model owns actions and effects.
type surfaceRow struct{ Label, Value string }
type surface struct {
	Title                           string
	Summary, Details, Hints, Status []string
	Rows                            []surfaceRow
	Selected                        int
	PinDetails                      bool
	Scrollable                      bool
}

func surfaceLabelWidth(rows []surfaceRow, width int) int {
	longest := 0
	for _, row := range rows {
		longest = max(longest, ansi.StringWidth(row.Label))
	}
	return max(1, min(longest, width-4-min(36, max(12, width/3))))
}

func (s surface) lines(width int) ([]string, int) {
	var lines []string
	for _, row := range s.Summary {
		lines = append(lines, Wrap(row, width)...)
	}
	selected := len(lines)
	labelWidth := surfaceLabelWidth(s.Rows, width)
	for i, row := range s.Rows {
		label := truncate(row.Label, labelWidth)
		text := label + strings.Repeat(" ", max(2, labelWidth+2-ansi.StringWidth(label))) + row.Value
		marker := "  "
		if i == s.Selected {
			selected = len(lines)
			marker = "❯ "
		}
		text = truncate(marker+text, width)
		if i == s.Selected {
			text = selectionRow(text, width)
		} else {
			text = Body.Render(text)
		}
		lines = append(lines, text)
	}
	if !s.PinDetails && len(s.Details) > 0 {
		lines = append(lines, SectionSt.Render("Selected details"))
		for _, row := range s.Details {
			lines = append(lines, Wrap(row, width)...)
		}
	}
	return lines, selected
}

func (s surface) view(width, height, scroll int, follow bool) string {
	if height < 3 {
		text := s.Title
		if s.Selected >= 0 && s.Selected < len(s.Rows) {
			text = s.Rows[s.Selected].Label + ": " + s.Rows[s.Selected].Value
		}
		return strings.Join(fitRows([]string{truncate(text, width)}, max(0, height)), "\n")
	}
	inner := BoxInner(width)
	hints := Wrap(strings.Join(s.Hints, "\n"), inner)
	var fixed, details []string
	for _, row := range s.Status {
		fixed = append(fixed, truncate(row, inner))
	}
	if s.PinDetails {
		for _, row := range s.Details {
			details = append(details, Wrap(row, inner)...)
		}
	}
	room := max(1, height-2-len(hints)-len(fixed))
	details = details[:min(len(details), max(0, room-3))]
	room = max(1, room-len(details))
	lines, selected := s.lines(inner)
	if follow {
		scroll = selected - room/2
	}
	start := min(max(0, scroll), max(0, len(lines)-room))
	body := append(fixed, lines[start:min(len(lines), start+room)]...)
	body = append(body, details...)
	title := itemTitle(s.Title, s.Selected, len(s.Rows))
	if s.Scrollable {
		title = fmt.Sprintf("%s · %d-%d/%d", s.Title, start+1, min(len(lines), start+room), len(lines))
	}
	return panel(title, body, hints, width, height)
}
