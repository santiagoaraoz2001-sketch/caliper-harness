package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"sort"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"strings"
)

type runtimeMetrics struct {
	Attempts, Failed, Tools, Generated, Reported int
	Models                                       []string
	Last                                         llm.RequestMeasurement
	Incomplete                                   bool
}

func (s *runtimeMetrics) observe(raw string) {
	var v llm.RequestMeasurement
	if json.Unmarshal([]byte(raw), &v) != nil {
		s.Incomplete = true
		return
	}
	s.Attempts++
	if v.Failed {
		s.Failed++
	}
	if v.Usage.Reported {
		s.Generated += v.Usage.Out
		s.Reported++
	}
	if v.Served != "" {
		found := false
		for _, name := range s.Models {
			found = found || name == v.Served
		}
		if !found {
			s.Models = append(s.Models, v.Served)
		}
	}
	s.Last = v
}

type runtimeMetricsMsg struct {
	Owner   origin
	Seq     int
	Metrics runtimeMetrics
}

func (m *Model) fetchRuntimeMetrics() tea.Cmd {
	if m.Chat.Running || m.Chat.Compacting {
		return nil
	}
	owner := m.origin()
	seq := m.Chat.Seq
	ids := append([]string(nil), m.Chat.SessionIDs...)
	dir := m.SessionsPath
	incomplete := m.Chat.Runtime.Incomplete
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		totals := runtimeMetrics{Incomplete: incomplete}
		for _, id := range ids {
			events, err := session.Read(dir, id)
			if err != nil {
				totals.Incomplete = true
				continue
			}
			calls := 0
			hasReply := false
			for _, e := range events {
				switch e.Kind {
				case "model_request":
					totals.observe(e.Text)
					calls++
				case "tool":
					totals.Tools++
				case "reply", "native_reply", "partial", "native-response":
					hasReply = true
				}
			}
			if calls == 0 && hasReply {
				totals.Incomplete = true
			}
		}
		return runtimeMetricsMsg{Owner: owner, Seq: seq, Metrics: totals}
	}
}
func (m *Model) renderRuntimeStatus(w, h int) string {
	s := m.Chat.Runtime
	if len(m.Chat.SessionIDs) > 0 && s.Attempts == 0 {
		s.Incomplete = true
	}
	rows := []string{Title.Render("Runtime status"), Guidance.Render("Current conversation · retained recorded Coding client requests")}
	state, _ := m.activityState()
	if !m.modelWorkActive() && m.Chat.Dialog == nil {
		state = "Idle · " + state
	}
	rows = append(rows, "Activity: "+state)
	generated := fmt.Sprintf("%d server-reported", s.Generated)
	if s.Attempts == 0 {
		generated = "unknown · no recorded requests"
	} else if s.Reported < s.Attempts || s.Incomplete {
		generated += " · partial; unreported/history unknown"
	}
	rows = append(rows, "Generated tokens: "+generated, fmt.Sprintf("Model request attempts: %d · failed: %d · Tool call attempts: %d", s.Attempts, s.Failed, s.Tools))
	rate := "unknown · no reported output and request duration"
	if s.Last.Usage.Reported && s.Last.Seconds > 0 {
		rate = fmt.Sprintf("%.2f tok/s · last request, includes prefill and transport", float64(s.Last.Usage.Out)/s.Last.Seconds)
	}
	rows = append(rows, "Request throughput: "+rate)
	models := append([]string(nil), s.Models...)
	sort.Strings(models)
	if len(models) == 0 {
		models = []string{"unknown · no served identity recorded"}
	}
	rows = append(rows, "Models used: "+strings.Join(models, ", "))
	if s.Incomplete {
		rows = append(rows, WarnSt.Render("Historical/failed recording coverage incomplete; counts are observed only"))
	}
	rows = append(rows, Guidance.Render("Excludes manual compaction, title and Judge requests"), Guidance.Render("Connections · last explicit endpoint health check"))
	seen := map[string]bool{}
	for _, r := range m.Serve.Rows {
		if r.IsModel || seen[r.Ep.URL] {
			continue
		}
		seen[r.Ep.URL] = true
		state := "unreachable"
		if r.Health.Up {
			state = "responding · server activity unknown"
		}

		role := ""
		if sameServeURL(r.Ep.URL, m.Cfg.Model.Server) {
			role += " [Coding]"
		}
		if !m.Cfg.Decision.Disabled && sameServeURL(r.Ep.URL, m.Cfg.Decision.Server) {
			role += " [Judge]"
		}
		rows = append(rows, r.Ep.Label+role+" · "+state+" · "+r.Ep.URL)
	}
	if len(seen) == 0 {
		rows = append(rows, "Unknown · no endpoint health checked")
	}
	return panel("Status", Wrap(strings.Join(rows, "\n"), BoxInner(w)), []string{m.ownedHint("Read-only · /manage Models & Servers · /chat · " + m.escapeHint())}, w, h)
}
