package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/decision"
	"specific-harness/internal/session"
)

// DecisionRow is one verdict from this chat's runs: what the
// decision model picked, how sure it was, what the harness did.
type DecisionRow struct {
	ConfidenceSource string
	Source           string
	Pick             string
	Conf             float64
	Resolution       string
	Session          string
}

// DecisionsState is the T6.7 feed: this chat's verdicts newest
// first, per-source counts, confidence buckets, malformed skips.
type DecisionsState struct {
	Rows     []DecisionRow
	BySource map[string]int
	Buckets  [4]int
	Skipped  int
	Sessions int
	scroll   int
}

// decisionsLoadedMsg carries a finished feed read.
type decisionsLoadedMsg struct {
	state DecisionsState
	err   error
}

func (m *Model) decisionsActive() bool {
	return m.Tabs[m.Active].at.view == ViewDecisions
}

// bucketOf mirrors the judgments CLI edges: <0.5, 0.5–0.7,
// 0.7–0.9, >=0.9.
func bucketOf(conf float64) int {
	switch {
	case conf < 0.5:
		return 0
	case conf < 0.7:
		return 1
	case conf < 0.9:
		return 2
	default:
		return 3
	}
}

// refreshDecisions reads this chat's runs off the event loop, newest
// run first. Unreadable sessions and malformed lines skip with a
// count — never silent, never blocking the feed.
func (m *Model) refreshDecisions() tea.Cmd {
	dir := m.SessionsPath
	ids := append([]string(nil), m.Chat.SessionIDs...)
	limit := m.Cfg.UI.MaxDecisionRows
	return func() tea.Msg {
		var st DecisionsState
		st.BySource = map[string]int{}
		for i := len(ids) - 1; i >= 0; i-- {
			events, err := session.Read(dir, ids[i])
			if err != nil {
				st.Skipped++
				continue
			}
			st.Sessions++
			for j := len(events) - 1; j >= 0; j-- {
				e := events[j]
				if e.Kind != decision.KindJudgment {
					continue
				}
				j, ok := decision.ParseJudgment(e.Text)
				if !ok {
					st.Skipped++
					continue
				}
				if len(st.Rows) >= limit {
					continue
				}
				st.Rows = append(st.Rows, DecisionRow{
					Source: j.Source, Pick: j.Pick,
					Conf: j.Confidence, Resolution: j.Resolution,
					Session: ids[i], ConfidenceSource: j.ConfidenceSource,
				})
				st.BySource[j.Source]++
				st.Buckets[bucketOf(j.Confidence)]++
			}
		}
		return decisionsLoadedMsg{state: st}
	}
}

// onDecisionsLoaded swaps in the fresh feed, loud on failure.
func (m *Model) onDecisionsLoaded(msg decisionsLoadedMsg) {
	if msg.err != nil {
		m.alert("err", msg.err.Error())
		return
	}
	msg.state.scroll = m.Decisions.scroll
	m.Decisions = msg.state
}

func (m Model) decisionsLayout(w, vh int) (body, hints []string, page int) {
	var lines []string
	lines = append(lines, Title.Render("Decisions — this chat's verdicts"))
	if len(m.Decisions.Rows) == 0 {
		lines = append(lines, Muted.Render("no judgments yet — risky runs and judge calls land here"))
	} else {
		var srcs []string
		for s, n := range m.Decisions.BySource {
			srcs = append(srcs, fmt.Sprintf("%s %d", s, n))
		}
		sort.Strings(srcs)
		b := m.Decisions.Buckets
		lines = append(lines, Body.Render(fmt.Sprintf("%d verdicts · %s", len(m.Decisions.Rows), strings.Join(srcs, " · ")))+
			Muted.Render(fmt.Sprintf(" · conf <0.5:%d 0.5–0.7:%d 0.7–0.9:%d ≥0.9:%d", b[0], b[1], b[2], b[3])))
		var confidenceSources = map[string]int{}
		for _, r := range m.Decisions.Rows {
			confidenceSources[decision.ConfidenceLabel(r.ConfidenceSource)]++
		}
		var labels []string
		for source, count := range confidenceSources {
			labels = append(labels, fmt.Sprintf("%s %d", source, count))
		}
		sort.Strings(labels)
		lines = append(lines, Muted.Render("Confidence: "+strings.Join(labels, " · ")),
			Muted.Render("Pooled scores: sources are not comparable; unspecified provenance is unknown."))
		lines = append(lines, SectionSt.Render("Source · Choice · Confidence · Resolution"))
		for _, r := range m.Decisions.Rows {
			lines = append(lines, PathSt.Render(r.Source)+Muted.Render(" → ")+Title.Render(r.Pick)+InfoSt.Render(fmt.Sprintf(" · %.2f [%s]", r.Conf, decision.ConfidenceLabel(r.ConfidenceSource)))+Body.Render(" · "+r.Resolution))
		}
	}
	if m.Decisions.Skipped > 0 {
		lines = append(lines, Muted.Render(fmt.Sprintf("%d unreadable or malformed — see judgments <id>", m.Decisions.Skipped)))
	}
	inner := BoxInner(w)
	hints = Wrap(Muted.Render("pgup/pgdn scroll · home/end · auto-refreshes · "+m.escapeHint()), inner)
	return Wrap(strings.Join(lines, "\n"), inner), hints, max(1, vh-2-len(hints))
}

func (m *Model) clampDecisions() {
	lines, _, page := m.decisionsLayout(m.Width, viewHeight(*m))
	m.Decisions.scroll = min(max(0, m.Decisions.scroll), max(0, len(lines)-page))
}

func (m Model) decisionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	lines, _, page := m.decisionsLayout(m.Width, viewHeight(m))
	switch msg.Type {
	case tea.KeyPgUp:
		m.Decisions.scroll -= page
	case tea.KeyPgDown:
		m.Decisions.scroll += page
	case tea.KeyHome:
		m.Decisions.scroll = 0
	case tea.KeyEnd:
		m.Decisions.scroll = len(lines)
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) renderDecisions(w, vh int) string {
	lines, hints, page := m.decisionsLayout(w, vh)
	start := min(max(0, m.Decisions.scroll), max(0, len(lines)-page))
	end := min(len(lines), start+page)
	body := append([]string(nil), lines[start:end]...)
	for len(body) < page {
		body = append(body, "")
	}
	body = append(body, hints...)
	return Box(fmt.Sprintf("Decisions · %d-%d/%d", start+1, end, len(lines)), strings.Join(body, "\n"), w)
}
