package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAccentToken(t *testing.T) {
	if Accent != "#4AF6C3" {
		t.Errorf("accent=%q, want the approved mark green", Accent)
	}
}

func TestStylesRenderContent(t *testing.T) {
	for name, got := range map[string]string{
		"tab":    TabActive.Render("X"),
		"title":  Title.Render("X"),
		"err":    ErrSt.Render("X"),
		"prompt": Prompt.Render("X"),
	} {
		if !strings.Contains(got, "X") {
			t.Errorf("%s drops its content: %q", name, got)
		}
	}
	if !strings.Contains(HRule(4), "─") {
		t.Errorf("rule has no rule char: %q", HRule(4))
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short=%q", got)
	}
	if got := truncate("hello world", 6); got != "hello…" {
		t.Errorf("long=%q", got)
	}
}

func TestBox(t *testing.T) {
	got := Box("T", "hi", 20)
	for _, want := range []string{"T", "hi", "┌", "┐", "└", "┘", "│"} {
		if !strings.Contains(got, want) {
			t.Errorf("box omits %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w != 20 {
			t.Errorf("box line width %d, want 20: %q", w, line)
		}
	}
}

func TestBoxStyledTitle(t *testing.T) {
	// Raw codes: lipgloss strips styles without a TTY, so a
	// Render-based title could never exercise the width math.
	got := Box("T\x1b[38;2;136;136;136m hint\x1b[0m", "hi", 20)
	for _, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w != 20 {
			t.Errorf("styled title breaks the frame, width %d: %q", w, line)
		}
	}
}

func TestPadRight(t *testing.T) {
	if got := PadRight("ab", "cd", 8); got != "ab    cd" {
		t.Errorf("pad=%q", got)
	}
	if got := PadRight("a", "\x1b[38;2;136;136;136mb\x1b[0m", 5); lipgloss.Width(got) != 5 {
		t.Errorf("styled width=%d, want 5: %q", lipgloss.Width(got), got)
	}
	if got := PadRight("abcdef", "gh", 6); got != "abcdef" {
		t.Errorf("narrow must drop right: %q", got)
	}
}
