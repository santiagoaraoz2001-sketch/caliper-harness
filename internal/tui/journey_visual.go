package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
)

func (m Model) dockRows() []string {
	if m.Tabs[m.Active].at.view != ViewChat || m.HelpOpen || m.Chat.Dialog != nil || len(m.suggestions()) > 0 || m.Width < 64 || m.Height < 20 {
		return nil
	}
	nodes := m.dockNodes()
	if len(nodes) == 0 {
		return nil
	}
	// A dock replaces the existing branch footer row. Leave at least six chat
	// rows, and reclaim decoration for short/multiline/alert-heavy frames.
	extra := min(2, max(0, m.Height-len(m.tabChrome())-(m.Input.Height()+2)-3-1-6))
	if len(m.Alerts) > 0 {
		extra--
	}
	if chipsView(m) != "" {
		extra--
	}
	if extra < 1 {
		return nil
	}
	state := m.Tabs[m.Active].dock
	pos := 0
	found := state.Selected == ""
	for i, r := range nodes {
		if r.ID == state.Selected {
			pos = i
			found = true
		}
	}
	branch := m.St.Branch
	if branch == "" {
		branch = "workspace"
	}
	parent := m.Journey.Tree.Plans[nodes[pos].PI]
	hint := "↓ select"
	if state.Focused {
		hint = "↑↓ select · enter open · esc input"
	}
	if !found {
		hint = "selected workflow unavailable · ↑↓ select"
	}
	heading := PathSt.Render(branch) + dot() + Guidance.Render(truncate(parent.Ref+" "+parent.P.Title, max(8, m.Width-len(hint)-len(branch)-6))) + dot() + Guidance.Render(hint)
	if m.Journey.Err != "" {
		heading = WarnSt.Bold(false).Render(truncate("Plans snapshot stale · /plans to refresh", m.Width))
	}
	var list []string
	for _, r := range nodes {
		prefix := "  "
		if r.Kind == rowApproach {
			prefix = "  └─ "
		}
		title := r.Ref + " " + r.Label
		status := r.State
		if r.Kind == rowApproach {
			status += fmt.Sprintf(" · %d/%d qualified", r.Done, r.Total)
		}
		row := PadRight(prefix+truncate(title, max(8, m.Width-ansi.StringWidth(status)-len(prefix)-2)), Guidance.Render(status), m.Width)
		if state.Focused && found && r.ID == state.Selected {
			row = selectionRow("❯ "+strings.TrimPrefix(row, "  "), m.Width)
		} else {
			row = Guidance.Render(row)
		}
		list = append(list, truncate(row, m.Width))
	}
	return append([]string{truncate(heading, m.Width)}, listWindow(list, pos, extra)...)
}
func (m Model) dockExtraHeight() int {
	if rows := m.dockRows(); len(rows) > 0 {
		return len(rows) - 1
	}
	return 0
}
func (m Model) journeyBreadcrumb(r *PlanRow) string {
	if r == nil {
		return "Workflow inspection"
	}
	lp := m.Journey.Tree.Plans[r.PI]
	text := lp.Ref + " " + lp.P.Title
	if r.AI >= 0 {
		la := lp.Approaches[r.AI]
		text += " › " + la.Ref + " " + la.A.Title
	}
	if r.TI >= 0 {
		text += " › " + r.Ref + " " + r.Label
	}
	return text
}
func (m Model) journeyHeader(w int) []string {
	at := m.Tabs[m.Active].at
	r := m.journeyNode(at.target)
	inner := BoxInner(w)
	if r == nil {
		return []string{WarnSt.Bold(false).Render("Selected workflow is unavailable · esc returns")}
	}
	var lines []string
	breadcrumb := m.journeyBreadcrumb(r)
	if w < 100 || ansi.StringWidth(breadcrumb) > inner {
		lp := m.Journey.Tree.Plans[r.PI]
		refs := lp.Ref
		if r.AI >= 0 {
			refs += " › " + lp.Approaches[r.AI].Ref
		}
		if r.TI >= 0 {
			refs += " › " + r.Ref
		}
		lines = append(lines, Guidance.Render(truncate(refs, inner)))
	} else {
		lines = append(lines, PathSt.Render(breadcrumb))
	}
	f := at.journey
	summary := r.State
	switch f.Kind {
	case rowPlan:
		summary += fmt.Sprintf(" · %d/%d tasks qualified", r.Done, r.Total)
	case rowApproach:
		lp := m.Journey.Tree.Plans[r.PI]
		la := lp.Approaches[r.AI]
		wf := wfRow(lp.P.Title, la.A, la.Tasks)
		summary += fmt.Sprintf(" · %d/%d tasks qualified · %d running tasks", wf.Done, wf.Total, wf.Agents)
	case rowTask:
		task := m.journeyTask(r)
		summary += fmt.Sprintf(" · execution=%s · verification=%s · qualified=%t", task.Execution, task.Verification, task.Qualified())
	case "attempt":
		summary = fmt.Sprintf("Attempt %d · recorded history · entries in stored order", f.Attempt)
		if f.Attempt == 0 {
			summary = "Legacy / unassigned recorded history"
		}
	case "receipt", "rawrecord":
		summary = fmt.Sprintf("Record #%d · attempt %d · historical inspection", f.Record, f.Attempt)
	case "brief":
		summary = "Full brief & current recorded result"
	case "rawlog":
		summary = "Raw attempt log · inspection"
	}
	for _, line := range Wrap(summary, inner) {
		lines = append(lines, Guidance.Render(line))
	}
	intent := ""
	if f.Kind == rowPlan {
		intent = m.Journey.Tree.Plans[r.PI].P.Goal
	}
	if f.Kind == rowTask {
		intent = m.journeyTask(r).Current
	}
	if intent != "" {
		lines = append(lines, Body.Render(truncate("Goal: "+oneLine(intent), inner)))
	}
	if m.Journey.Err != "" {
		lines = append(lines, WarnSt.Bold(false).Render(truncate("Last snapshot · refresh failed: "+m.Journey.Err, inner)))
	}
	if f.Kind == rowTask {
		_, records, err := readJourneyRecords(m.journeyTask(r))
		if err != nil {
			lines = append(lines, WarnSt.Bold(false).Render(truncate(err.Error(), inner)))
		} else if len(records) == 0 {
			lines = append(lines, Guidance.Render("No attempt records retained"))
		}
	}
	if choices := m.journeyChoices(); len(choices) > 0 && m.selectedJourneyChoice(choices) == nil {
		lines = append(lines, WarnSt.Bold(false).Render(truncate("Selection unavailable · ↑↓ chooses another row", inner)))
	}
	return lines
}
func (m Model) journeyHints(w int) []string {
	f := m.Tabs[m.Active].at.journey
	text := "↑↓ select · enter open · f comment · r refresh · esc back"
	if len(m.journeyChoices()) == 0 {
		text = "↑↓/pgup/pgdn scroll · f comment · r refresh · esc back"
	}
	if f.Kind == "receipt" {
		text = "↑↓/pgup/pgdn scroll · enter raw record · f comment · r refresh · esc back"
	}
	if f.Kind == rowApproach {
		done := "hidden"
		if m.Wf.ShowDone {
			done = "shown"
		}
		text += " · l launch · p pause/resume · c complete:" + done
	}
	return Wrap(m.ownedHint(text), BoxInner(w))
}
func (m Model) journeyLayout(w, h int) ([]string, []string, int) {
	hints := m.journeyHints(w)
	hints = hints[:min(len(hints), max(0, h-3))]
	header := m.journeyHeader(w)
	cap := max(0, h-2-len(hints)-1)
	if len(header) > cap {
		header = nil
		if r := m.journeyNode(m.Tabs[m.Active].at.target); r != nil && cap > 0 {
			header = append(header, PathSt.Render(truncate(r.Ref+" "+r.Label, BoxInner(w))))
			if cap > 1 {
				state := "Current node: " + r.State
				if task := m.journeyTask(r); task != nil {
					state = fmt.Sprintf("Current task: %s · qualified=%t", task.State, task.Qualified())
				}
				f := m.Tabs[m.Active].at.journey
				if f.Kind == "attempt" || f.Kind == "receipt" || f.Kind == "rawrecord" {
					state = fmt.Sprintf("Historical attempt %d", f.Attempt)
					if f.Kind != "attempt" {
						state += fmt.Sprintf(" · record #%d", f.Record)
					}
				}
				if m.Journey.Err != "" {
					state = "Snapshot stale · " + state
				}
				header = append(header, Guidance.Render(truncate(state, BoxInner(w))))
			}
		}
	}
	return header, hints, max(1, h-2-len(header)-len(hints))
}
func (m Model) journeyBodyHeight(w, h int) int {
	if h < 4 {
		return 1 // Borderless compact details show one source row, then padding.
	}
	_, _, room := m.journeyLayout(w, h)
	return room
}
func (m *Model) clampJourneyScroll() {
	if !m.journeyActive() || len(m.journeyChoices()) > 0 {
		return
	}
	frame := &m.Tabs[m.Active].at.journey
	limit := max(0, len(m.journeyDetailRows(m.Width))-m.journeyBodyHeight(m.Width, viewHeight(*m)))
	frame.Scroll = min(max(0, frame.Scroll), limit)
}
func (m Model) journeyDetailRows(w int) []string {
	at := m.Tabs[m.Active].at
	r := m.journeyNode(at.target)
	f := at.journey
	inner := BoxInner(w)
	if r == nil {
		return Wrap("This node was removed or retired. Escape restores the previous layer; r retries the snapshot.", inner)
	}
	task := m.journeyTask(r)
	switch f.Kind {
	case "attempt":
		_, _, err := readJourneyRecords(task)
		if err != nil {
			return Wrap(err.Error()+" · esc returns to raw log access", inner)
		}
		return Wrap("No retained receipts for this attempt · esc returns", inner)
	case "brief":
		d := &PlanDetail{Kind: r.Kind, ID: r.ID, Ref: r.Ref}
		lp := m.Journey.Tree.Plans[r.PI]
		switch r.Kind {
		case rowPlan:
			d.LP = &lp
		case rowApproach:
			la := lp.Approaches[r.AI]
			d.LA = &la
		case rowTask:
			d.T = task
		}
		ancestry := Wrap(m.journeyBreadcrumb(r), inner)
		return append(ancestry, detailDisplay(d, inner)...)
	case "rawlog":
		return Numbered(strings.Split(task.AttemptLog, "\n"), inner)
	case "receipt", "rawrecord":
		raw, records, err := readJourneyRecords(task)
		if err != nil {
			return Wrap(err.Error()+" · esc returns to raw log access", inner)
		}
		if f.Record < 0 || f.Record >= len(raw) || recordDigest(raw[f.Record]) != f.Digest || records[f.Record].Attempt != f.Attempt {
			return Wrap("Selected recorded receipt changed or is unavailable. Escape and reopen it from the current attempt list.", inner)
		}
		if f.Kind == "rawrecord" {
			var pretty bytes.Buffer
			if json.Indent(&pretty, raw[f.Record], "", "  ") == nil {
				return Numbered(strings.Split(pretty.String(), "\n"), inner)
			}
			return Numbered([]string{string(raw[f.Record])}, inner)
		}
		record := records[f.Record]
		out := []string{Title.Render(record.Kind), journeyStatus(recordState(record))}
		if record.Truncated {
			out = append(out, WarnSt.Bold(false).Render("Saved evidence is truncated · enter raw record for retained data"))
		}
		saved := false
		for _, phase := range []struct {
			name   string
			events []session.Event
		}{{"Execution", record.ExecutionEvents}, {"Check", record.CheckEvents}, {"Reviewer", record.ReviewerEvents}} {
			if len(phase.events) == 0 {
				continue
			}
			saved = true
			out = append(out, SectionSt.Render(phase.name+" · saved events"))
			out = append(out, journeyEventRows(phase.events, inner)...)
		}
		if !saved {
			out = append(out, Guidance.Render("No saved phase events · enter opens the complete raw record"))
		}
		return out
	}
	return nil
}
func journeyStatus(text string) string {
	style := Guidance
	if strings.Contains(text, string(outcome.Failed)) || strings.Contains(text, "rejected") {
		style = ErrSt.Bold(false)
	} else if strings.Contains(text, "unverified") || strings.Contains(text, "recording") {
		style = WarnSt.Bold(false)
	}
	return style.Render(text)
}

// Friendly saved-event projection; Enter still exposes the exact full record.
// Unknown/malformed envelopes fall back to their literal retained text.
func journeyEventRows(events []session.Event, inner int) []string {
	var rows []string
	for _, event := range events {
		var entry session.Entry
		if (event.Kind != "native_result" && event.Kind != "native_reply") || json.Unmarshal([]byte(event.Text), &entry) != nil || entry.Message.Role == "" {
			rows = append(rows, recordedEventRows([]session.Event{event}, inner)...)
			continue
		}
		name := entry.Message.Name
		if name == "" {
			name = entry.Message.Role
		}
		style := PathSt
		if entry.Kind == "failure" {
			style = ErrSt.Bold(false)
		}
		for _, row := range Wrap(name+" · "+entry.Kind+" · "+entry.Message.ToolCallID, inner) {
			rows = append(rows, style.Render(row))
		}
		for _, thought := range []string{entry.Message.Thinking, entry.Message.Reasoning, entry.Message.ReasoningContent} {
			if thought != "" {
				for _, row := range Wrap(thought, inner) {
					rows = append(rows, ThinkingSt.Render(row))
				}
			}
		}
		for _, row := range Wrap(entry.Message.Content, inner) {
			rows = append(rows, Body.Render(row))
		}
		for _, call := range entry.Message.ToolCalls {
			for _, row := range Wrap(call.Name+" · "+call.ID+" · "+call.Arguments, inner) {
				rows = append(rows, PathSt.Render(row))
			}
		}
	}
	return rows
}
func (m Model) renderJourney(w, h int) string {
	at := m.Tabs[m.Active].at
	r := m.journeyNode(at.target)
	choices := m.journeyChoices()
	header, hints, room := m.journeyLayout(w, h)
	if h < 4 {
		text := "Workflow unavailable · esc back"
		if c := m.selectedJourneyChoice(choices); c != nil {
			text = c.label
		} else if rows := m.journeyDetailRows(w); len(rows) > 0 {
			start := min(max(0, at.journey.Scroll), len(rows)-1)
			text = rows[start]
		}
		return strings.Join(fitRows([]string{truncate(text, w)}, h), "\n")
	}
	var body []string
	if len(choices) > 0 {
		selected := m.selectedJourneyChoice(choices)
		pos := 0
		var rows []string
		for i, c := range choices {
			row := PadRight("  "+truncate(c.label, max(8, BoxInner(w)-ansi.StringWidth(c.state)-4)), journeyStatus(c.state), BoxInner(w))
			if selected != nil && c.id == selected.id {
				pos = i
				row = selectionRow("❯ "+strings.TrimPrefix(row, "  "), BoxInner(w))
			} else {
				row = Body.Render(row)
			}
			rows = append(rows, truncate(row, BoxInner(w)))
		}
		body = listWindow(rows, pos, room)
	} else {
		body = m.journeyDetailRows(w)
		if len(body) == 0 {
			body = []string{Guidance.Render("No recorded children or evidence · r refresh · esc back")}
		}
		start := min(max(0, at.journey.Scroll), max(0, len(body)-room))
		body = body[start:min(len(body), start+room)]
	}
	title := "Workflow summary"
	if r != nil {
		title = r.Label
	}
	return panel(title, append(header, body...), hints, w, h)
}
