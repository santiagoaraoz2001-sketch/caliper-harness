package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
	"testing"
)

func TestStartupMarkUsesOnlyASCIIAndFits(t *testing.T) {
	profile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(profile)
	for _, p := range []termenv.Profile{termenv.Ascii, termenv.TrueColor} {
		lipgloss.SetColorProfile(p)
		for _, cols := range []int{9, 19, 36, 70} {
			mark := StartupMark(cols)
			for _, row := range mark {
				plain := sgrSequence.ReplaceAllString(row, "")
				if lipgloss.Width(row) != cols {
					t.Fatalf("bad width: %q", row)
				}
				for _, r := range plain {
					if r > 127 {
						t.Fatalf("non-ASCII logo glyph %q", r)
					}
				}
			}
			if !strings.Contains(strings.Join(mark, "\n"), strings.Repeat(";", cols)) {
				t.Fatal("missing canonical corner")
			}
		}
	}
	for _, w := range []int{20, 40, 80, 120} {
		for _, row := range strings.Split(StartupHeader("0.0.1", "a long workspace name", w), "\n") {
			if lipgloss.Width(row) > w {
				t.Fatalf("header exceeds%d: %q", w, row)
			}
		}
	}
}
