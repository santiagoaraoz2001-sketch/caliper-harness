package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StartupMark keeps the user's filled ASCII treatment: a white solid corner
// and mint solid circle, with transparent negative space. Seventy columns is
// the requested full size; narrower terminals scale the same silhouette.
// Terminal cells are taller than they are wide, so rows account for that.
// This mark is startup-only; chat retains its simple category dots.
func StartupMark(cols int) []string {
	cols = max(1, min(70, cols))
	rows := max(1, int(math.Round(float64(cols)*3/7)))
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	mint := lipgloss.NewStyle().Foreground(lipgloss.Color(Accent))
	out := make([]string, rows)
	for row := 0; row < rows; row++ {
		var line strings.Builder
		// Color only the filled shape; negative space stays transparent.
		last := byte(' ')
		var segment strings.Builder
		flush := func() {
			switch last {
			case 'w':
				line.WriteString(white.Render(segment.String()))
			case 'm':
				line.WriteString(mint.Render(segment.String()))
			default:
				line.WriteString(segment.String())
			}
			segment.Reset()
		}
		for col := 0; col < cols; col++ {
			x := (float64(col) + .5) * 120 / float64(cols)
			y := (float64(row) + .5) * 120 / float64(rows)
			kind, glyph := byte(' '), byte(' ')
			switch {
			case y < 24 || x >= 96:
				kind, glyph = 'w', ';'
			case (x-38)*(x-38)+(y-82)*(y-82) <= 34*34:
				kind, glyph = 'm', ';'
			}
			if kind != last {
				flush()
				last = kind
			}
			segment.WriteByte(glyph)
		}
		flush()
		out[row] = line.String()
	}
	return out
}

func StartupHeader(version, workspace string, width int) string {
	cols := min(70, max(1, width-4))
	mark := StartupMark(cols)
	rows := make([]string, 0, len(mark)+5)
	for _, row := range mark {
		rows = append(rows, "  "+row)
	}
	rows = append(rows, "", "  "+Title.Render(truncate("Caliper Harness", cols)),
		"  "+Guidance.Render(truncate("by Specific Labs · "+version, cols)),
		"", "  "+Guidance.Render(truncate("Workspace "+workspace, cols)))
	return strings.Join(rows, "\n") + "\n\n"
}
