// Package tui is the interactive terminal app: tab shell, views, and the
// slash registry. One binary, in-process over internal/.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Specific Labs mint identity and affordances, neutral prose/guidance,
// and Blueprint state colors. Other hues are confined to code syntax.
const (
	Accent        = "#4AF6C3"
	Ink           = "#E8E8E8"
	Dim           = "#888888"
	Faint         = "#555555"
	ErrHue        = activityRed
	Cyan          = Accent       // Identity and informational affordances use brand mint.
	Blue          = activityBlue // Reserved for Blueprint status and code syntax.
	WarnHue       = activityAmber
	Shade         = "#2A2A2A"
	SyntaxString  = "#D9C9A0"
	SyntaxNumber  = "#8FBDF0"
	SyntaxComment = "#8E989D"
)

var (
	TabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Accent)).Underline(true)
	TabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color(Dim))
	Title       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Ink))
	Body        = lipgloss.NewStyle().Foreground(lipgloss.Color(Ink))
	Muted       = lipgloss.NewStyle().Foreground(lipgloss.Color(Dim))
	Guidance    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7A817D"))
	FaintSt     = lipgloss.NewStyle().Foreground(lipgloss.Color(Faint))
	ErrSt       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(ErrHue))
	WarnSt      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(WarnHue))
	InfoSt      = lipgloss.NewStyle().Foreground(lipgloss.Color(Cyan))
	PathSt      = lipgloss.NewStyle().Foreground(lipgloss.Color(Accent))
	ThinkingSt  = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color(Dim))
	SectionSt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Accent))
	SelectedSt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Ink)).Background(lipgloss.Color(Shade))
	AddSt       = lipgloss.NewStyle().Foreground(lipgloss.Color("#BAE8C5")).Background(lipgloss.Color("#203C30"))
	RemoveSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F0BDC2")).Background(lipgloss.Color("#482A2D"))
	UserSt      = lipgloss.NewStyle().Foreground(lipgloss.Color(Accent)).Background(lipgloss.Color(Shade))
	Prompt      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Accent))
	PickerSel   = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color(Accent))
	Rule        = lipgloss.NewStyle().Foreground(lipgloss.Color(Faint))
)

// HRule draws a thin full-width separator.
func HRule(w int) string {
	if w < 1 {
		w = 1
	}
	return Rule.Render(strings.Repeat("─", w))
}

// truncate caps s at n cells with an ellipsis.
func truncate(s string, n int) string { return ansi.Truncate(s, max(0, n), "…") }

// BoxInner is the content width Box uses for frame w.
func BoxInner(w int) int {
	return max(1, w-4)
}

// PadRight joins left and right with space padding to width
// cells, dropping right when nothing fits. Widths are visible.
func PadRight(left, right string, width int) string {
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		return left
	}
	return left + strings.Repeat(" ", pad) + right
}

// Box wraps content in a titled square box of width w.
func Box(title, content string, w int) string {
	inner := BoxInner(w)
	w = inner + 4
	dashes := inner - 1 - lipgloss.Width(title)
	if dashes < 0 {
		title = truncate(title, inner-1)
		dashes = 0
	}
	var b strings.Builder
	b.WriteString(FaintSt.Render("┌─ ") + Body.Render(title) + FaintSt.Render(" "+strings.Repeat("─", dashes)+"┐") + "\n")
	for _, line := range strings.Split(content, "\n") {
		pad := inner - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(FaintSt.Render("│ ") + truncate(line, inner) + FaintSt.Render(strings.Repeat(" ", pad)+" │") + "\n")
	}
	b.WriteString(FaintSt.Render("└" + strings.Repeat("─", inner+2) + "┘"))
	return b.String()
}
