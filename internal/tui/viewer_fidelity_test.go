package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/plan"
	"specific-harness/internal/watch"
)

func fidelityModel(view ViewID) Model {
	m := New(testCfg(), "test")
	m.Width, m.Height = 34, 20
	lines := []string{"pre " + strings.Repeat("ABCDEFGHI", 30) + " end", "TARGET_TWO"}
	for i := 3; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("TARGET_%d", i))
	}
	m.Files.Viewer = &FileView{Title: "fixture.txt", Logical: lines}
	m.Files.Root, m.Files.Path = "root", "fixture.txt"
	m.Diff.Viewer = &DiffViewer{Ev: watch.Event{Path: "fixture.txt", After: lines}}
	m.Plans.Detail = &PlanDetail{Kind: rowTask, Ref: "T1", T: &plan.Task{Title: lines[0], State: "TARGET_TWO", Current: strings.Join(lines[2:], "\n")}}
	m.Plans.Loaded = true
	m.navigate(view, "")
	return m
}

func fidelityBody(m *Model) string {
	switch m.Tabs[m.Active].at.view {
	case ViewFiles:
		return ansi.Strip(strings.Join(m.renderFileViewer(m.Width, viewHeight(*m)), "\n"))
	case ViewDiff:
		return ansi.Strip(strings.Join(m.viewerLines(m.Width, viewHeight(*m)), "\n"))
	default:
		return ansi.Strip(strings.Join(m.detailLines(m.Width, viewHeight(*m)), "\n"))
	}
}
func fidelityScroll(m *Model) *int {
	switch m.Tabs[m.Active].at.view {
	case ViewFiles:
		return &m.Files.Viewer.Scroll
	case ViewDiff:
		return &m.Diff.Viewer.Scroll
	default:
		return &m.Plans.Scroll
	}
}
func fidelityRow(logical []string, inner, line int) int {
	prefix := fmt.Sprintf("%*d  ", len(fmt.Sprint(len(logical))), line)
	for i, s := range Numbered(logical, inner) {
		if strings.HasPrefix(ansi.Strip(s), prefix) {
			return i
		}
	}
	return -1
}

func TestViewerFidelitySourceAndQuote(t *testing.T) {
	logical := []string{"  pre ABCDEFGHIJK end  ", "\treturn πé界  \r", "", "```literal\\n  "}
	for _, width := range []int{16, 32, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			rows := Numbered(logical, width)
			reconstructed := make([]string, len(logical))
			line := -1
			for _, row := range rows {
				s := ansi.Strip(row)
				if len(s) < 3 {
					t.Fatalf("short row %q", s)
				}
				if s[0] != ' ' {
					line++
				}
				if line < 0 {
					t.Fatalf("missing logical label %q", s)
				}
				reconstructed[line] += s[3:]
				if ansi.StringWidth(s) > width {
					t.Fatalf("overwidth row %q", s)
				}
			}
			for i, raw := range logical {
				want := strings.ReplaceAll(strings.TrimSuffix(raw, "\r"), "\t", "    ")
				if reconstructed[i] != want {
					t.Errorf("line%d display %q want%q", i+1, reconstructed[i], want)
				}
			}
			quote, err := Quote(logical, "[1-4]")
			if err != nil || quote != strings.Join(logical, "\n") {
				t.Fatalf("raw quote=%q err%v", quote, err)
			}
		})
	}
}

func TestViewerFidelityGotoResizeAndQuote(t *testing.T) {
	for _, view := range []ViewID{ViewFiles, ViewDiff, ViewPlans} {
		t.Run(string(view), func(t *testing.T) {
			m := fidelityModel(view)
			_, logical, _ := m.activeFile()
			m = submit(m, "/goto 2")
			prefix := fmt.Sprintf("%*d  ", len(fmt.Sprint(len(logical))), 2)
			if !strings.Contains(fidelityBody(&m), prefix+strings.Split(logical[1], " · ")[0]) {
				t.Fatalf("goto2 missing target:\n%s", fidelityBody(&m))
			}
			m = submit(m, "/comment [2] exact")
			if len(m.Comment) != 1 || m.Comment[0].Quote != logical[1] {
				t.Fatalf("quote%+v", m.Comment)
			}
			m.Alerts = nil
			*fidelityScroll(&m) = fidelityRow(logical, BoxInner(m.Width), 20)
			next, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
			m = next.(Model)
			if *fidelityScroll(&m) != fidelityRow(logical, BoxInner(m.Width), 20) {
				t.Fatalf("resize lost logical20:scroll%d", *fidelityScroll(&m))
			}
			next, _ = m.Update(tea.WindowSizeMsg{Width: 34, Height: 20})
			m = next.(Model)
			if *fidelityScroll(&m) != fidelityRow(logical, BoxInner(m.Width), 20) {
				t.Fatal("narrow resize lost logical20")
			}
			m = submit(m, "/goto 999")
			if !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, fmt.Sprintf("line %d", len(logical))) {
				t.Fatalf("untruthful clamp:%+v", m.Alerts)
			}
			if !strings.Contains(fidelityBody(&m), logical[len(logical)-1]) {
				t.Fatal("final target not visible")
			}
			screen := strings.Split(ansi.Strip(m.View()), "\n")
			if !strings.Contains(strings.Join(screen[:min(len(screen), m.Height)], "\n"), logical[len(logical)-1]) {
				t.Fatal("final target clipped from actual public View")
			}

		})
	}
}

func TestViewerFidelityRefreshAndInactiveResize(t *testing.T) {
	m := fidelityModel(ViewFiles)
	_, logical, _ := m.activeFile()
	m.Files.Ticket = 7
	*fidelityScroll(&m) = fidelityRow(logical, BoxInner(m.Width), 20)
	updated := append([]string(nil), logical...)
	updated[0] = "short"
	next, _ := m.Update(filesOpenedMsg{root: "root", title: "fixture.txt", ticket: 7, lines: updated})
	m = next.(Model)
	if *fidelityScroll(&m) != fidelityRow(updated, BoxInner(m.Width), 20) {
		t.Fatal("refresh lost logical20")
	}
	m = key(m, "ctrl+t")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	m = next.(Model)
	m = key(m, "ctrl+p")
	if !strings.Contains(fidelityBody(&m), "20  TARGET_20") {
		t.Fatalf("inactive resize/navigation lost anchor:\n%s", fidelityBody(&m))
	}
}

func TestViewerFidelityLargeDiffHeader(t *testing.T) {
	m := fidelityModel(ViewDiff)
	m.Diff.Viewer.Ev.Added = make([]string, 50)
	for i := range m.Diff.Viewer.Ev.Added {
		m.Diff.Viewer.Ev.Added[i] = "added"
	}
	m = submit(m, "/goto 2")
	body := fidelityBody(&m)
	if !strings.Contains(body, "TARGET_TWO") || !strings.Contains(body, "summary rows omitted") {
		t.Fatalf("header hid source or omission:\n%s", body)
	}
}

func TestViewerFidelityPublicNormalWidths(t *testing.T) {
	for _, view := range []ViewID{ViewFiles, ViewDiff, ViewPlans} {
		for _, width := range []int{80, 120} {
			t.Run(fmt.Sprintf("%s/%d", view, width), func(t *testing.T) {
				m := fidelityModel(view)
				next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				m = next.(Model)
				_, logical, _ := m.activeFile()
				m = submit(m, "/goto 999")
				rows := strings.Split(ansi.Strip(m.View()), "\n")
				shown := strings.Join(rows[:min(len(rows), m.Height)], "\n")
				if !strings.Contains(shown, logical[len(logical)-1]) {
					t.Fatalf("last target clipped:%s", shown)
				}
				next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
				m = next.(Model)
				if !strings.Contains(m.View(), logical[len(logical)-1]) {
					t.Fatal("resize target missing")
				}
			})
		}
	}
}
