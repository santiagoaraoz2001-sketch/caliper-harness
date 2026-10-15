package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

// Frames are values in the existing per-tab back/forward history. No execution
// authority, mutable plan pointers or drafts are stored in navigation.
type journeyFrame struct {
	Kind, Selected, Digest  string
	Scroll, Attempt, Record int
}
type dockState struct {
	Focused  bool
	Selected string
}
type journeyState struct {
	Tree    PlansState
	Request uint64
	Loaded  bool
	Err     string
}
type journeyLoadedMsg struct {
	plans              []LoadedPlan
	request            uint64
	owner, path, route string
	err                error
}
type journeyChoice struct {
	id, label, state string
	frame            journeyFrame
	target           string
}
type journeyRecord struct {
	Truncated                                    bool
	Kind                                         string
	Attempt                                      int
	Outcome                                      outcome.Outcome
	Verification                                 outcome.Verification
	Comment                                      *outcome.TaskComment
	ExecutionEvents, CheckEvents, ReviewerEvents []session.Event
}

func (m Model) journeyActive() bool { return m.Tabs[m.Active].at.view == ViewJourney }
func (m Model) journeyRoute() string {
	at := m.Tabs[m.Active].at
	return fmt.Sprintf("%s:%s:%s:%d:%d:%s", at.view, at.target, at.journey.Kind, at.journey.Attempt, at.journey.Record, at.journey.Digest)
}
func (m Model) journeyRead() tea.Cmd {
	request, owner, path, route, pool := m.Journey.Request, m.Chat.ID, m.StorePath, m.journeyRoute(), uint64(m.Cfg.Store.BufferPoolBytes)
	return func() tea.Msg {
		plans, err := loadPlans(path, pool)
		return journeyLoadedMsg{plans, request, owner, path, route, err}
	}
}
func (m *Model) refreshJourney() tea.Cmd { m.Journey.Request++; return m.journeyRead() }
func (m *Model) onJourneyLoaded(msg journeyLoadedMsg) {
	if msg.request != m.Journey.Request || msg.owner != m.Chat.ID || msg.path != m.StorePath || msg.route != m.journeyRoute() {
		return
	}
	if msg.err != nil {
		m.Journey.Err = msg.err.Error()
		return
	}
	tree := PlansState{Plans: msg.plans}
	tree.rebuild("")
	m.Journey.Tree, m.Journey.Loaded, m.Journey.Err = tree, true, ""
}
func (m Model) journeyNode(id string) *PlanRow {
	for _, r := range m.Journey.Tree.Nodes {
		if r.ID == id && !m.Journey.Tree.retired(r) {
			return &r
		}
	}
	return nil
}
func (m Model) dockNodes() []PlanRow {
	var rows []PlanRow
	for _, r := range m.Journey.Tree.Nodes {
		if r.Kind != rowTask && !m.Journey.Tree.retired(r) && (m.Wf.ShowDone || r.Kind != rowApproach || r.State != plan.ApproachComplete) {
			rows = append(rows, r)
		}
	}
	return rows
}
func (m *Model) openJourney(c journeyChoice) {
	m.navigate(ViewJourney, c.target)
	m.Tabs[m.Active].at.journey = c.frame
	if children := m.journeyChoices(); len(children) > 0 {
		m.Tabs[m.Active].at.journey.Selected = children[0].id
	}
	m.syncJourneyFocus()
}
func (m Model) journeyTask(r *PlanRow) *plan.Task {
	if r == nil || r.Kind != rowTask {
		return nil
	}
	return m.Journey.Tree.Plans[r.PI].Approaches[r.AI].Tasks[r.TI]
}
func readJourneyRecords(t *plan.Task) ([]json.RawMessage, []journeyRecord, error) {
	if t == nil || strings.TrimSpace(t.AttemptLog) == "" {
		return nil, nil, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(t.AttemptLog), "[") {
		return nil, nil, fmt.Errorf("Attempt evidence unreadable: expected a stored record array")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(t.AttemptLog), &raw); err != nil {
		return nil, nil, fmt.Errorf("Attempt evidence unreadable: %v", err)
	}
	records := make([]journeyRecord, len(raw))
	for i, b := range raw {
		if err := json.Unmarshal(b, &records[i]); err != nil || records[i].Kind == "" || records[i].Attempt < 0 {
			return nil, nil, fmt.Errorf("Attempt evidence unreadable at record #%d", i)
		}
		if records[i].Kind == "comment" {
			c := records[i].Comment
			if c == nil || c.TaskID != t.ID || c.Attempt <= 0 {
				return nil, nil, fmt.Errorf("Comment provenance unavailable at record #%d", i)
			}
			records[i].Attempt = c.Attempt
		}
	}
	return raw, records, nil
}
func recordDigest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func recordState(r journeyRecord) string {
	if strings.HasSuffix(r.Kind, "_pending") {
		return "recording · no final receipt"
	}
	state := string(r.Outcome.Status)
	if state == "" {
		state = "outcome not recorded"
	}
	if r.Verification != "" {
		state += " · verification=" + string(r.Verification)
	}
	return state
}
func (m Model) journeyChoices() []journeyChoice {
	at := m.Tabs[m.Active].at
	r := m.journeyNode(at.target)
	f := at.journey
	if r == nil {
		return nil
	}
	var choices []journeyChoice
	switch f.Kind {
	case rowPlan, rowApproach:
		for _, c := range m.Journey.Tree.Nodes {
			match := f.Kind == rowPlan && c.Kind == rowApproach && c.PI == r.PI || f.Kind == rowApproach && c.Kind == rowTask && c.PI == r.PI && c.AI == r.AI
			if !match || m.Journey.Tree.retired(c) || (!m.Wf.ShowDone && c.Kind == rowApproach && c.State == plan.ApproachComplete) {
				continue
			}
			state := c.State
			if c.Kind == rowApproach {
				state += fmt.Sprintf(" · %d/%d qualified", c.Done, c.Total)
			} else {
				state += " · " + c.Execution + "/" + c.Verification
			}
			choices = append(choices, journeyChoice{c.ID, c.Ref + " " + c.Label, state, journeyFrame{Kind: c.Kind}, c.ID})
		}
		choices = append(choices, journeyChoice{"brief", "Full brief", "inspection", journeyFrame{Kind: "brief"}, r.ID})
	case rowTask:
		_, records, err := readJourneyRecords(m.journeyTask(r))
		if err == nil {
			seen := map[int]bool{}
			for _, rec := range records {
				if seen[rec.Attempt] {
					continue
				}
				seen[rec.Attempt] = true
				label := fmt.Sprintf("Attempt %d", rec.Attempt)
				if rec.Attempt == 0 {
					label = "Legacy / unassigned records"
				}
				count := 0
				for _, other := range records {
					if other.Attempt == rec.Attempt {
						count++
					}
				}
				choices = append(choices, journeyChoice{fmt.Sprintf("attempt:%d", rec.Attempt), label, fmt.Sprintf("%d recorded entries", count), journeyFrame{Kind: "attempt", Attempt: rec.Attempt}, r.ID})
			}
		}
		choices = append(choices, journeyChoice{"brief", "Full brief & current result", "inspection", journeyFrame{Kind: "brief"}, r.ID})
		if err != nil {
			choices = append(choices, journeyChoice{"rawlog", "Raw attempt log", "unreadable evidence", journeyFrame{Kind: "rawlog"}, r.ID})
		}
	case "attempt":
		raw, records, err := readJourneyRecords(m.journeyTask(r))
		if err != nil {
			return nil
		}
		for i, rec := range records {
			if rec.Attempt != f.Attempt {
				continue
			}
			digest := recordDigest(raw[i])
			id := fmt.Sprintf("record:%d:%s", i, digest)
			choices = append(choices, journeyChoice{id, fmt.Sprintf("Record #%d · %s", i, rec.Kind), recordState(rec), journeyFrame{Kind: "receipt", Attempt: f.Attempt, Record: i, Digest: digest}, r.ID})
		}
	}
	return choices
}
func (m Model) selectedJourneyChoice(choices []journeyChoice) *journeyChoice {
	if len(choices) == 0 {
		return nil
	}
	selected := m.Tabs[m.Active].at.journey.Selected
	if selected == "" {
		return &choices[0]
	}
	for _, c := range choices {
		if c.id == selected {
			return &c
		}
	}
	return nil
}
func (m *Model) moveJourney(delta int) {
	choices := m.journeyChoices()
	if len(choices) == 0 {
		return
	}
	pos := -1
	if c := m.selectedJourneyChoice(choices); c != nil {
		for i := range choices {
			if choices[i].id == c.id {
				pos = i
			}
		}
	}
	pos = min(max(0, pos+delta), len(choices)-1)
	m.Tabs[m.Active].at.journey.Selected = choices[pos].id
}
func (m Model) journeyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.Chat.Dialog != nil || len(m.suggestions()) > 0 {
		return m, nil, false
	}
	if m.journeyActive() {
		if m.Input.Value() != "" {
			return m, nil, false
		}
		choices := m.journeyChoices()
		frame := &m.Tabs[m.Active].at.journey
		switch msg.Type {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			d := 1
			if msg.Type == tea.KeyUp || msg.Type == tea.KeyPgUp {
				d = -1
			}
			if msg.Type == tea.KeyPgUp || msg.Type == tea.KeyPgDown {
				d *= m.journeyBodyHeight(m.Width, viewHeight(m))
			}
			if len(choices) > 0 {
				m.moveJourney(d)
			} else {
				frame.Scroll = max(0, frame.Scroll+d)
			}
			return m, nil, true
		case tea.KeyHome:
			if len(choices) > 0 {
				m.moveJourney(-len(choices))
			} else {
				frame.Scroll = 0
			}
			return m, nil, true
		case tea.KeyEnd:
			if len(choices) > 0 {
				m.moveJourney(len(choices))
			} else {
				frame.Scroll = 1 << 30
			}
			return m, nil, true
		case tea.KeyEnter:
			if c := m.selectedJourneyChoice(choices); c != nil {
				m.Tabs[m.Active].at.journey.Selected = c.id
				m.openJourney(*c)
			} else if frame.Kind == "receipt" && m.journeyNode(m.Tabs[m.Active].at.target) != nil {
				f := *frame
				f.Kind = "rawrecord"
				f.Scroll = 0
				m.openJourney(journeyChoice{frame: f, target: m.Tabs[m.Active].at.target})
			}
			return m, nil, true
		}
		if msg.Type == tea.KeyRunes && !msg.Alt {
			if msg.String() == "r" {
				return m, m.refreshJourney(), true
			}
			if msg.String() == "f" {
				if r := m.journeyNode(m.Tabs[m.Active].at.target); r != nil {
					m.commentPrefill(r.Ref)
				}
				return m, nil, true
			}
			if msg.String() == "l" || msg.String() == "p" || msg.String() == "c" {
				at := m.Tabs[m.Active].at
				if at.journey.Kind == rowApproach {
					if r := m.journeyNode(at.target); r != nil {
						lp := m.Journey.Tree.Plans[r.PI]
						la := lp.Approaches[r.AI]
						row := wfRow(lp.P.Title, la.A, la.Tasks)
						switch msg.String() {
						case "l":
							if row.State != plan.ApproachReady {
								m.alert("info", "launch needs a ready approach")
								return m, nil, true
							}
							return m, m.wfLaunch(row, false), true
						case "p":
							if row.State == plan.ApproachRunning {
								return m, m.wfPause(&row), true
							}
							if row.State == plan.ApproachPaused {
								return m, m.wfLaunch(row, true), true
							}
							m.alert("info", "pause needs running; resume needs paused")
							return m, nil, true
						case "c":
							m.Wf.ShowDone = !m.Wf.ShowDone
							return m, m.refreshWorkflows(), true
						}
					}
				}
			}
		}
		return m, nil, false
	}
	if m.Tabs[m.Active].at.view != ViewChat || msg.Alt {
		return m, nil, false
	}
	dock := &m.Tabs[m.Active].dock
	if dock.Focused && m.Input.Value() == "" {
		nodes := m.dockNodes()
		pos := -1
		for i, r := range nodes {
			if r.ID == dock.Selected {
				pos = i
			}
		}
		switch msg.Type {
		case tea.KeyUp:
			if pos <= 0 {
				dock.Focused = false
				m.Input.Focus()
			} else {
				dock.Selected = nodes[pos-1].ID
			}
			return m, nil, true
		case tea.KeyDown:
			if len(nodes) > 0 {
				dock.Selected = nodes[min(pos+1, len(nodes)-1)].ID
			}
			return m, nil, true
		case tea.KeyEnter:
			if r := m.journeyNode(dock.Selected); r != nil {
				m.openJourney(journeyChoice{frame: journeyFrame{Kind: r.Kind}, target: r.ID})
			}
			return m, nil, true
		}
	}
	if msg.Type == tea.KeyDown && m.Input.Value() == "" && len(m.dockRows()) > 0 {
		nodes := m.dockNodes()
		if len(nodes) > 0 {
			dock.Focused = true
			if dock.Selected == "" {
				dock.Selected = nodes[0].ID
			}
			m.Input.Blur()
			return m, nil, true
		}
	}
	if dock.Focused && (msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace || msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlJ) {
		dock.Focused = false
		m.Input.Focus()
	}
	return m, nil, false
}

func (m *Model) syncJourneyFocus() {
	dock := &m.Tabs[m.Active].dock
	if dock.Focused && m.Tabs[m.Active].at.view == ViewChat && !m.HelpOpen && m.Chat.Dialog == nil && (m.Input.Value() != "" || len(m.dockRows()) == 0) {
		dock.Focused = false
	}
	if m.askFreeText() {
		dock.Focused = false
		m.Input.Focus()
		return
	}
	if m.HelpOpen || m.Chat.Dialog != nil {
		return
	}
	if m.Tabs[m.Active].at.view == ViewChat && m.Tabs[m.Active].dock.Focused {
		if m.Input.Focused() {
			m.Input.Blur()
		}
	} else if !m.Input.Focused() {
		m.Input.Focus()
	}
}
