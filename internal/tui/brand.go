package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// specificMark rasterizes the canonical 120×120 circle-and-corner geometry
// into two columns on one row. The compact turn anchor shares the same geometry.
// Source: Brand Viewer/assets/mark.svg, preserved beside this implementation.
func specificMark(color string) string {
	bits := [4][2]int{{0, 3}, {1, 4}, {2, 5}, {6, 7}}
	var row strings.Builder
	for cx := 0; cx < 2; cx++ {
		mask := 0
		for dy := 0; dy < 4; dy++ {
			for dx := 0; dx < 2; dx++ {
				x, y := float64((cx*2+dx)*30)+15, float64(dy*30)+15
				circle := (x-38)*(x-38)+(y-82)*(y-82) <= 34*34
				if y < 24 || x >= 96 || circle {
					mask |= 1 << bits[dy][dx]
				}
			}
		}
		row.WriteRune(rune(0x2800 + mask))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(row.String())
}

// selectionRow reapplies the background after every nested SGR reset. Padding
// and styled foreground segments therefore form one continuous selected row.
func selectionRow(row string, w int) string {
	return shadedRow(row, w, SelectedSt, Shade)
}

// shadedRow keeps one surface beneath styled text, markers and trailing cells.
// An inner color reset must not punch holes in the outer background.
func shadedRow(row string, w int, style lipgloss.Style, color string) string {
	row = truncate(row, w)
	row += strings.Repeat(" ", max(0, w-ansi.StringWidth(row)))
	rendered := style.Render(row)
	if !strings.Contains(rendered, "\x1b[") {
		return rendered // NO_COLOR and plain terminal profiles stay plain.
	}
	background := lipgloss.NewStyle().Background(lipgloss.Color(color)).Render(" ")
	// Reuse the renderer's negotiated color profile instead of emitting truecolor
	// unconditionally. The first sequence here contains only the background.
	end := strings.Index(background, "m")
	if end < 0 {
		return rendered
	}
	bg := background[:end+1]
	return sgrSequence.ReplaceAllStringFunc(rendered, func(s string) string { return s + bg }) + "\x1b[0m"
}
