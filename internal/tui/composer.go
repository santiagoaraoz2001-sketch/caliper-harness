package tui

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/tui/thirdparty/textarea"
	"strings"
	"unicode"
)

// Canonicalize newline spelling once; keep literal tabs, indentation and edges.
type composerSanitizer struct{}

func (composerSanitizer) Sanitize(in []rune) []rune {
	out := make([]rune, 0, len(in))
	for i, r := range in {
		if r == '\r' {
			if i+1 < len(in) && in[i+1] == '\n' {
				continue
			}
			r = '\n'
		}
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			out = append(out, r)
		}
	}
	return out
}
func canonicalInput(s string) string { return string((composerSanitizer{}).Sanitize([]rune(s))) }
func newComposer() textarea.Model {
	m := textarea.New()
	m.Prompt = "❯ "
	m.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "❯ "
		}
		return "  "
	})
	m.ShowLineNumbers = false
	m.CharLimit = 0
	m.MaxHeight = 0
	m.MaxWidth = 0
	m.Placeholder = "ask anything, or / for commands"
	m.FocusedStyle.Prompt = Prompt
	m.FocusedStyle.Text = Body
	m.FocusedStyle.CursorLine = Body
	m.FocusedStyle.Placeholder = FaintSt
	m.BlurredStyle.Text = FaintSt
	m.BlurredStyle.CursorLine = FaintSt
	m.SetSanitizer(composerSanitizer{})
	m.KeyMap.InsertNewline = keybind.NewBinding(keybind.WithKeys("ctrl+j"))
	m.Focus()
	return m
}

// Keep one prompt at the top visible row even when the draft itself scrolls.
// The textarea still owns all editing and cursor positions in its two-cell gutter.
func (m Model) composerView() string {
	rows := strings.Split(m.Input.View(), "\n")
	style := FaintSt
	if m.Input.Focused() {
		style = Prompt
	}
	rows[0] = style.Render("❯ ") + ansi.Cut(rows[0], 2, ansi.StringWidth(rows[0]))
	return strings.Join(rows, "\n")
}

// Resize only on state changes, never View: textarea shares its viewport pointer.
func (m *Model) resizeComposer() {
	if m.Input.Err != nil {
		m.alert("err", m.Input.Err.Error())
		m.Input.Err = nil
	}
	m.Input.SetWidth(BoxInner(m.Width))
	rows := m.Input.LineCount() + m.Input.LineInfo().Height - 1
	reserved := 8 + max(0, len(m.tabChrome())-2) + pickerLines(*m) + m.dockExtraHeight()
	if m.Tabs[m.Active].at.view == ViewChat {
		reserved++
	}
	if len(m.Alerts) > 0 {
		reserved++
	}
	if chipsView(*m) != "" {
		reserved++
	}
	if d := dialogView(*m); d != "" {
		reserved += strings.Count(d, "\n") + 1
	}
	m.Input.SetHeight(min(5, max(1, min(rows, m.Height-reserved))))
}
