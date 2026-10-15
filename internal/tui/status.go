package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"specific-harness/internal/config"
)

// Status is the footer: session config plus live polled bits. Chat
// feeds CtxUsed from T6.2; the tick feeds branch and plan.
type Status struct {
	Auth           string
	CtxUsed        int
	CtxKnown       bool
	CtxWindow      int
	Think          bool
	Model          string
	Branch         string
	Dir            string
	PlanTitle      string
	PlanApproaches []string
}

// ShortAuth maps config modes to status labels.
func ShortAuth(mode string) string {
	switch mode {
	case config.AuthYOLO:
		return "YOLO"
	case config.AuthRisky:
		return "RISKY"
	default:
		return "ALL"
	}
}

// statusTickMsg re-arms the live footer fetch; statusLiveMsg carries it.
type statusTickMsg struct{}

type statusLiveMsg struct {
	branch, dir  string
	haveGit      bool
	planTitle    string
	planApproach []string
	havePlan     bool
}

// StatusInterval polls branch + plan this often.
const StatusInterval = 5 * time.Second

func tickStatus() tea.Cmd {
	return tea.Tick(StatusInterval, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// fetchLive reads branch and plan off the event loop. Failures mark
// their segment stale (the last good value stays) — never loud.
func fetchLive(storePath string, bufferPoolBytes uint64) tea.Cmd {
	return func() tea.Msg {
		m := statusLiveMsg{}
		if b, d, err := gitInfo(""); err == nil {
			m.branch, m.dir, m.haveGit = b, d, true
		}
		if t, a, ok := planStatus(storePath, bufferPoolBytes); ok {
			m.planTitle, m.planApproach, m.havePlan = t, a, true
		}
		return m
	}
}

// kfmt renders token counts dense: 950 → "950", 12400 → "12.1k".
func kfmt(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d", n)
	}
	return strings.Replace(fmt.Sprintf("%.1fk", float64(n)/1024), ".0k", "k", 1)
}

// ctxBar renders an 8-cell usage bar as filled + empty runs.
func ctxBar(used, window int) (filled, empty string) {
	f := 0
	if window > 0 {
		f = used * 8 / window
		if f < 0 {
			f = 0
		}
		if f > 8 {
			f = 8
		}
	}
	return strings.Repeat("▓", f), strings.Repeat("░", 8-f)
}

func dot() string {
	return FaintSt.Render(" · ")
}

// Render draws the three footer lines: status, plan, keys.
func (s Status) Render() string { return s.RenderWidth(0) }
func (s Status) RenderWidth(width int) string {
	var auth string
	switch s.Auth {
	case "YOLO":
		auth = ErrSt.Render("YOLO")
	case "RISKY":
		auth = WarnSt.Render("RISKY")
	default:
		auth = InfoSt.Render(s.Auth)
	}
	win := "—"
	if s.CtxWindow > 0 {
		win = kfmt(s.CtxWindow)
	}
	used := s.CtxUsed
	if !s.CtxKnown {
		used = 0
	}
	f, e := ctxBar(used, s.CtxWindow)
	value := "unknown"
	if s.CtxKnown {
		value = kfmt(s.CtxUsed)
	}
	ctx := Muted.Render("last measured prompt ") + Body.Render(value+"/"+win) + " " + InfoSt.Render(f) + FaintSt.Render(e)
	think := Muted.Render("THINK:") + Muted.Render("OFF requested")
	if s.Think {
		think = Muted.Render("THINK:") + InfoSt.Render("ON requested")
	}
	model := s.Model
	if model == "" {
		model = "—"
	}
	line1 := auth + dot() + ctx + dot() + think + dot() + InfoSt.Render(truncate(model, 32))
	if width >= 64 {
		ctx = Muted.Render("last measured prompt ") + Body.Render(value+"/"+win)
		fixed := auth + dot() + ctx + dot() + think + dot()
		modelRoom := max(1, width-lipgloss.Width(fixed))
		line1 = auth + dot() + InfoSt.Render(truncate(model, modelRoom)) + dot() + ctx + dot() + think
	}
	var left string
	switch {
	case s.Branch != "" && s.Dir != "":
		left = Body.Render(truncate(s.Branch, 20)) + dot() + Muted.Render(truncate(s.Dir, 20))
	case s.Dir != "":
		left = Muted.Render(truncate(s.Dir, 20))
	default:
		left = Muted.Render("no git")
	}
	right := Muted.Render("no plan")
	if s.PlanTitle != "" {
		right = Body.Render(truncate(s.PlanTitle, 24))
		for _, a := range s.PlanApproaches {
			right += dot() + Muted.Render("▸ "+a)
		}
	}
	line2 := left + dot() + right
	line3 := Muted.Render("esc back · /help")
	return line1 + "\n" + line2 + "\n" + line3
}

// footer derives the active conversation reading instead of a stale global cost.
func (m Model) footer() string {
	s := m.St
	c := m.Chat.Context
	s.CtxUsed = c.Prompt
	s.CtxKnown = c.Measured && c.ModelKey == m.contextKey()
	view := m.Chat.Tools
	if view == "" {
		view = "coding (8), /tools full"
	}
	return strings.Replace(s.RenderWidth(m.Width), "esc back · /help", m.escapeHint()+" · /help · tools: "+view, 1)
}
