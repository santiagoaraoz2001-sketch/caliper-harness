package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestContinuousActionSurfaces(t *testing.T) {
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	lipgloss.SetColorProfile(termenv.TrueColor)
	for name, row := range map[string]string{
		"selection": selectionRow("❯ "+PathSt.Render("file.go")+" "+InfoSt.Render("active"), 40),
		"user":      shadedRow("• "+UserSt.Render("A readable prompt")+" "+PathSt.Render("file.go"), 40, UserSt, Shade),
		"added":     diffSource("+ ", "return \"readable\" // comment", 40, true),
		"removed":   diffSource("− ", "return 42", 40, false),
	} {
		t.Run(name, func(t *testing.T) {
			if ansi.StringWidth(row) != 40 {
				t.Fatal("surface width changed")
			}
			bg := false
			for len(row) > 0 {
				if loc := sgrSequence.FindStringIndex(row); loc != nil && loc[0] == 0 {
					seq := row[:loc[1]]
					row = row[loc[1]:]
					if seq == "\x1b[0m" || seq == "\x1b[49m" {
						bg = false
					}
					if strings.Contains(seq, "48;2;") {
						bg = true
					}
					continue
				}
				if !bg {
					t.Fatal("unshaded text or padding cell")
				}
				_, n := utf8.DecodeRuneInString(row)
				row = row[n:]
			}
		})
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if strings.Contains(shadedRow("plain", 40, UserSt, Shade), "\x1b") {
		t.Fatal("plain profile emits color")
	}
}

func TestChatActionBuffersKeepLocalOutputsGrouped(t *testing.T) {
	m := journeyFixture(120, 40)
	m.Chat.appendLine(lineTool, `read {"path":"file.go"}`)
	m.Chat.appendLine(lineResult, "tool content")
	m.Chat.appendLine(lineAssistant, "Next action\ncontinued prose")
	rows := chatRows(&m, 117)
	for i, row := range rows {
		if strings.HasPrefix(row.kind, "dot|") && !strings.Contains(row.kind, "|result|") && i > 0 && rows[i-1].kind != "space" {
			t.Fatalf("action %s has no buffer", row.kind)
		}
		if strings.Contains(row.kind, "|result|") && i > 0 && rows[i-1].kind == "space" {
			t.Fatal("tool result detached from request")
		}
		if i > 0 && row.kind == "space" && rows[i-1].kind == "space" {
			t.Fatal("duplicate action buffers")
		}
	}
}

func TestChatBufferedThoughtAnchorsRemainDistinct(t *testing.T) {
	m := journeyFixture(80, 24)
	m.Chat.Thoughts = map[int][]recordedThought{1: {{Label: "Thinking", Text: "First block"}, {Label: "Reasoning", Text: "Second block"}}}
	rows := chatRows(&m, chatTextWidth(m.Width))
	seen := map[chatOrigin]bool{}
	for _, row := range rows {
		if row.kind != "space" {
			continue
		}
		if seen[row.origin] {
			t.Fatal("action buffers share scroll anchor")
		}
		seen[row.origin] = true
	}
}
