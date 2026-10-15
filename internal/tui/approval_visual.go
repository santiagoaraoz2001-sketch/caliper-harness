package tui

import "strings"

// Raw approval text remains complete; only existing Markdown fences/headings
// get visual hierarchy. No inferred operation, path or risk fields.
func approvalRows(prompt string, width int) []string {
	var rows []string
	fence := ""
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if fence == "" {
				fence = "```"
			} else {
				fence = ""
			}
			for _, row := range Wrap(line, width) {
				rows = append(rows, Guidance.Render(row))
			}
			continue
		}
		for _, row := range Wrap(line, width) {
			text := Body.Render(row)
			if fence != "" {
				text = sourceText(row)
			} else if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
				text = Title.Render(row)
			}
			rows = append(rows, text)
		}
	}
	return rows
}
