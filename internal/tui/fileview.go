package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// FileView is the shared file-viewer system: every viewer (plan
// dive, diff file body, files explorer) shows numbered logical lines
// with the same goto and quote-by-spec behavior. Viewers keep their
// own scroll; this file owns numbering, quoting, and specs.
type FileView struct {
	Title   string
	Notice  string
	Logical []string
	Scroll  int
}

// Numbered renders logical lines with 1-based numbers, wrapping long
// ones under their number. Shared by all three viewers.
func Numbered(logical []string, inner int) []string { return layoutLines(logical, inner).Rows }

type lineLayout struct {
	Rows   []string
	Starts []int
}

func layoutLines(logical []string, inner int) lineLayout {
	w := len(fmt.Sprint(len(logical)))
	var out lineLayout
	for i, ln := range logical {
		out.Starts = append(out.Starts, len(out.Rows))
		display := strings.ReplaceAll(strings.TrimSuffix(ln, "\r"), "\t", strings.Repeat(" ", ansi.StringWidth(Body.Render("\t"))))
		for j, wl := range strings.Split(ansi.Hardwrap(display, max(1, inner-w-3), true), "\n") {
			prefix := strings.Repeat(" ", w+2)
			if j == 0 {
				prefix = Muted.Render(fmt.Sprintf("%*d  ", w, i+1))
			}
			out.Rows = append(out.Rows, prefix+wl)
		}
	}
	return out
}

func (l lineLayout) clamp(scroll, avail int) int {
	return max(0, min(scroll, max(0, len(l.Rows)-avail)))
}
func (l lineLayout) gotoLine(n, avail int) int {
	if len(l.Starts) == 0 {
		return 0
	}
	return l.clamp(l.Starts[max(0, min(n-1, len(l.Starts)-1))], avail)
}
func (l lineLayout) topLine(scroll int) int {
	n := 0
	for i, start := range l.Starts {
		if start > scroll {
			break
		}
		n = i + 1
	}
	return n
}

// viewerLayout is transient: all viewers share source rows and their existing scroll.
func (m *Model) viewerLayout(view ViewID) (lineLayout, *int, int) {
	h := viewHeight(*m)
	switch view {
	case ViewFiles:
		if v := m.Files.Viewer; v != nil {
			return layoutLines(v.Logical, BoxInner(m.Width)), &v.Scroll, m.filesAvail(m.Width, h)
		}
	case ViewDiff, ViewChanges:
		if v := m.Diff.Viewer; v != nil {
			return layoutLines(viewerBody(v.Ev), BoxInner(m.Width)), &v.Scroll, viewerAvail(v.Ev, m.diffHeight(h, m.Width))
		}
	case ViewPlans:
		if d := m.Plans.Detail; d != nil {
			return layoutLines(detailLogical(d), BoxInner(m.Width)), &m.Plans.Scroll, detailAvail(m.plansHeight(h, m.Width))
		}
	}
	return lineLayout{}, nil, 0
}

// Quote extracts the logical lines named by a "[1-3, 5]" spec.
func Quote(logical []string, spec string) (string, error) {
	ranges, err := parseLineSpec(spec)
	if err != nil {
		return "", err
	}
	var picked []string
	for _, rg := range ranges {
		if rg[0] < 1 || rg[1] > len(logical) {
			return "", fmt.Errorf("lines %s out of range (1–%d)", spec, len(logical))
		}
		picked = append(picked, logical[rg[0]-1:rg[1]]...)
	}
	return strings.Join(picked, "\n"), nil
}

// GotoScroll clamps a scroll offset that puts logical line n (1-based)
// at the top of an avail-high window.
func GotoScroll(total, n, avail int) int {
	if n < 1 {
		n = 1
	}
	if n > total {
		n = total
	}
	max := total - avail
	if max < 0 {
		max = 0
	}
	at := n - 1
	if at > max {
		at = max
	}
	return at
}

// parseLineSpec parses "[120-122, 140]" into ranges.
func parseLineSpec(s string) ([][2]int, error) {
	if len(s) < 3 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("bad line range %q", s)
	}
	var out [][2]int
	for _, part := range strings.Split(s[1:len(s)-1], ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("bad line range %q", s)
		}
		lo, hi, ok := strings.Cut(part, "-")
		if !ok {
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("bad line range %q", s)
			}
			out = append(out, [2]int{n, n})
			continue
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || a < 1 || b < a {
			return nil, fmt.Errorf("bad line range %q", s)
		}
		out = append(out, [2]int{a, b})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bad line range %q", s)
	}
	return out, nil
}

// isLinesSpec reports a well-formed "[1-3, 5]" word.
func isLinesSpec(s string) bool {
	_, err := parseLineSpec(s)
	return err == nil
}

// looksLikeSpec reports a bracket word, well-formed or not, so typos
// fail loud instead of sliding into comment text.
func looksLikeSpec(s string) bool {
	return len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']'
}
