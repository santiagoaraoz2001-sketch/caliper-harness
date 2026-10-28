package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"regexp"
	"strings"
)

// Rules carry guidance in the separator without renaming any key or operation.
func hintRule(text string, w int) string {
	text = truncate(text, max(1, w-4))
	n := max(0, w-lipgloss.Width(text)-4)
	return Rule.Render("─ ") + Guidance.Render(text) + Rule.Render(" "+strings.Repeat("─", n+1))
}
func (m Model) tabChromeRows() []string {
	tabRow, _ := m.tabStrip()
	rows := []string{truncate(tabRow, m.Width)}
	groups := []string{"ctrl+t new tab · ctrl+n/p switch tab", "ctrl+w close tab · ctrl+o/g tab back/fwd"}
	full := strings.Join(groups, " · ")
	if lipgloss.Width(full) <= m.Width-4 {
		rows = append(rows, hintRule(full, m.Width))
	} else {
		for _, g := range groups {
			for _, l := range Wrap(g, max(1, m.Width-4)) {
				rows = append(rows, hintRule(l, m.Width))
			}
		}
	}
	return rows
}

type tabSpan struct{ index, start, end int }

func (m Model) tabStrip() (string, []tabSpan) {
	labels := make([]string, len(m.Tabs))
	for i, t := range m.Tabs {
		labels[i] = t.Title
		if t.feature != "" {
			labels[i] = titleOf(t.feature)
		}
		if labels[i] == "" {
			labels[i] = titleOf(t.at.view)
		}
	}
	compact := false
	build := func(start, end int) (string, []tabSpan) {
		var row strings.Builder
		var spans []tabSpan
		if start > 0 {
			row.WriteString(FaintSt.Render(fmt.Sprintf("…%d │ ", start)))
		}
		for i := start; i < end; i++ {
			if i > start {
				row.WriteString(FaintSt.Render(" │ "))
			}
			label := labels[i]
			if compact {
				label = truncate(label, max(1, m.Width-12))
			}
			at := ansi.StringWidth(row.String())
			style := TabInactive
			if i == m.Active {
				style = TabActive
			}
			row.WriteString(style.Render(label))
			spans = append(spans, tabSpan{i, at, at + ansi.StringWidth(label)})
		}
		if end < len(labels) {
			row.WriteString(FaintSt.Render(fmt.Sprintf(" │ %d…", len(labels)-end)))
		}
		return row.String(), spans
	}
	if row, spans := build(0, len(labels)); ansi.StringWidth(row) <= m.Width {
		return row, spans
	}
	compact = true
	start, end := m.Active, m.Active+1
	for {
		grew := false
		if start > 0 {
			if row, _ := build(start-1, end); ansi.StringWidth(row) <= m.Width {
				start--
				grew = true
			}
		}
		if end < len(labels) {
			if row, _ := build(start, end+1); ansi.StringWidth(row) <= m.Width {
				end++
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	row, spans := build(start, end)
	return truncate(row, m.Width), spans
}

func (m Model) tabChrome() []string {
	rows := m.tabChromeRows()
	activity := m.activityLabel()
	content := m
	content.Width = max(1, m.Width-lipgloss.Width(activity)-1)
	rows[0] = PadRight(content.tabChromeRows()[0], activity, m.Width)
	if m.Width >= 64 && m.Height >= 20 {
		rows = append(rows, "")
	}
	return rows
}
func borderTitle(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 3 {
		return left
	}
	return left + Rule.Render(" "+strings.Repeat("─", gap-2)+" ") + right
}
func listWindow(rows []string, selected, room int) []string {
	room = max(1, room)
	if len(rows) <= room {
		return rows
	}
	start := min(max(0, selected-room/2), len(rows)-room)
	return rows[start : start+room]
}
func fitRows(rows []string, room int) []string {
	room = max(0, room)
	if len(rows) > room {
		rows = rows[:room]
	}
	for len(rows) < room {
		rows = append(rows, "")
	}
	return rows
}
func panel(title string, body, hints []string, w, h int) string {
	if h < 3 {
		if len(body) == 0 {
			body = []string{title}
		}
		return strings.Join(fitRows(body, max(0, h)), "\n")
	}
	inner := BoxInner(w)
	var keys []string
	for _, key := range hints {
		keys = append(keys, Wrap(key, inner)...)
	}
	if len(keys) > max(0, h-3) {
		keys = keys[:max(0, h-3)]
	}
	body = fitRows(body, max(0, h-2-len(keys)))
	for _, key := range keys {
		body = append(body, Guidance.Render(key))
	}
	return Box(title, strings.Join(body, "\n"), w)
}
func resultStyle(text string) lipgloss.Style {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"error:", "tool error:", "bash error:", "model error:", "run failed", "failed:"} {
		if strings.HasPrefix(lower, p) {
			return ErrSt.Bold(false)
		}
	}
	for _, p := range []string{"refused", "denied", "unverified", "cancelled", "stopped", "partial reply", "warning:"} {
		if strings.HasPrefix(lower, p) {
			return WarnSt.Bold(false)
		}
	}
	return Muted
}

var sourceTokens = regexp.MustCompile(`(?m)("(?:[^"\\]|\\.)*"|'[^']*'|#.*$|//.*$|\b(?:func|return|if|else|for|while|def|class|import|from|const|let|var|nil|true|false|None|True|False|echo|fi|then)\b|\b[0-9]+(?:\.[0-9]+)?\b)`)

var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func sourceText(text string) string {
	text = displayLineEndings(text)
	return sourceTokens.ReplaceAllStringFunc(text, func(token string) string {
		style := PathSt
		if strings.HasPrefix(token, "#") || strings.HasPrefix(token, "//") {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(SyntaxComment)).Italic(true)
		} else if strings.HasPrefix(token, "\"") || strings.HasPrefix(token, "'") {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(SyntaxString))
		} else if token[0] >= '0' && token[0] <= '9' {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(SyntaxNumber))
		}
		return style.Render(token)
	})
}
func diffSource(prefix, text string, w int, added bool) string {
	text = displayLineEndings(text)
	base := RemoveSt
	bg := "#482A2D"
	if added {
		base = AddSt
		bg = "#203C30"
	}
	text = ansi.Truncate(text, max(1, w-lipgloss.Width(prefix)), "…")
	var row strings.Builder
	row.WriteString(base.Render(prefix))
	last := 0
	for _, span := range sourceTokens.FindAllStringIndex(text, -1) {
		row.WriteString(base.Render(text[last:span[0]]))
		token := text[span[0]:span[1]]
		color := Accent
		if strings.HasPrefix(token, "#") || strings.HasPrefix(token, "//") {
			color = SyntaxComment
		} else if strings.HasPrefix(token, "\"") || strings.HasPrefix(token, "'") {
			color = SyntaxString
		} else if token[0] >= '0' && token[0] <= '9' {
			color = SyntaxNumber
		}
		row.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(lipgloss.Color(bg)).Render(token))
		last = span[1]
	}
	row.WriteString(base.Render(text[last:]))
	row.WriteString(base.Render(strings.Repeat(" ", max(0, w-ansi.StringWidth(prefix+text)))))
	return shadedRow(row.String(), w, base, bg)
}

// Normalize display text before ANSI styling can hide a trailing carriage return.
// Captures and quoted source retain their original content and line indices.
func displayLineEndings(text string) string {
	return strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\r")
}
func (m Model) ownedHint(text string) string {
	if m.Chat.Dialog != nil && m.Chat.Dialog.Kind == "ask" {
		if m.askOptions() {
			return "↑↓ pick · tab comment · enter answer · " + m.escapeHint()
		}
		return "Enter answer · Ctrl+J newline · " + m.escapeHint()
	}
	return text
}
func itemTitle(title string, selected, total int) string {
	if total > 0 {
		return fmt.Sprintf("%s · %d/%d", title, selected+1, total)
	}
	return title
}

func composerBox(left, right, content string, w int) string {
	title := borderTitle(Guidance.Render(left), Guidance.Render(right), BoxInner(w)-1)
	box := Box(title, content, w)
	if lipgloss.Width(left)+lipgloss.Width(right)+3 > BoxInner(w)-1 {
		rows := strings.Split(box, "\n")
		text := truncate(right, max(1, w-6))
		pad := max(0, w-lipgloss.Width(text)-5)
		rows[len(rows)-1] = Rule.Render("└"+strings.Repeat("─", pad)+" ") + Guidance.Render(text) + Rule.Render(" ─┘")
		return strings.Join(rows, "\n")
	}
	return box
}

var proseBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)

func proseText(text string) string {
	if strings.HasPrefix(text, "# ") || strings.HasPrefix(text, "## ") || strings.HasPrefix(text, "### ") {
		return Title.Render(strings.TrimLeft(text, "# "))
	}
	return proseBold.ReplaceAllStringFunc(text, func(part string) string { return Title.Render(strings.Trim(part, "*")) })
}
