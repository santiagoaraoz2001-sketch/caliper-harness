package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/launch"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/run"
	"specific-harness/internal/store"
)

// WfRow is one approach on the ops floor: execution state plus the
// hot facts — progress, attempts, live agents, and the step holding
// the work.
type WfRow struct {
	Approach                                                   string
	Plan                                                       string
	Title                                                      string
	State                                                      string
	Done                                                       int
	Executed, Verified, Unverified, Failed, Stopped, Cancelled int
	Total                                                      int
	Attempts                                                   int
	Agents                                                     int
	Step                                                       string
}

// WorkflowsState is the T6.8 monitor: activity-sorted rows, selection,
// a complete-toggle, a pending dive, and one in-flight run.
type WorkflowsState struct {
	Rows     []WfRow
	Sel      int
	ShowDone bool
	Dive     string
	Busy     string
	cancel   context.CancelFunc
	ctx      context.Context
	Err      string
}

// wfLoadedMsg carries a finished refresh; wfDoneMsg one finished run.
type wfLoadedMsg struct {
	rows []WfRow
	err  error
}

type wfDoneMsg struct {
	approach string
	ctx      context.Context
	text     string
	note     string
	report   *outcome.Report
	err      error
}

func (m *Model) workflowsActive() bool {
	return m.Tabs[m.Active].at.view == ViewWorkflows
}

// wfWeight sorts the floor by motion: running, paused, ready, draft,
// complete last.
func wfWeight(state string) int {
	switch state {
	case plan.ApproachRunning:
		return 0
	case plan.ApproachPaused:
		return 1
	case plan.ApproachReady:
		return 2
	case plan.ApproachDraft:
		return 3
	default:
		return 4
	}
}

// refreshWorkflows reads every approach off the event loop, hottest
// first. Store failures land loud on the view, never silent.
func (m *Model) refreshWorkflows() tea.Cmd {
	path, pool := m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes)
	showDone := m.Wf.ShowDone
	return func() tea.Msg {
		s, err := store.OpenWithBufferPool(path, pool)
		if err != nil {
			return wfLoadedMsg{err: err}
		}
		defer s.Close()
		plans, err := plan.ActivePlans(s)
		if err != nil {
			return wfLoadedMsg{err: err}
		}
		var rows []WfRow
		for _, p := range plans {
			approaches, err := plan.PlanApproaches(s, p.ID)
			if err != nil {
				return wfLoadedMsg{err: err}
			}
			for _, a := range approaches {
				tasks, err := plan.ApproachTasks(s, a.ID)
				if err != nil {
					return wfLoadedMsg{err: err}
				}
				row := wfRow(p.Title, a, tasks)
				if a.State == plan.ApproachComplete && row.Total > 0 && row.Done == row.Total && !showDone {
					continue
				}
				rows = append(rows, row)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return wfWeight(rows[i].State) < wfWeight(rows[j].State)
		})
		return wfLoadedMsg{rows: rows}
	}
}

// wfRow shapes one approach: done/total, summed attempts, and the
// holding step — the running task, else the next queued one.
func wfRow(planTitle string, a *plan.Approach, tasks []*plan.Task) WfRow {
	r := WfRow{Approach: a.ID, Plan: planTitle, Title: approachLabel(a.Title), State: a.State, Total: len(tasks)}
	for _, t := range tasks {
		r.Attempts += t.Attempts
		if t.Qualified() {
			r.Done++
		}
		if t.Execution == outcome.Completed {
			r.Executed++
			if t.Verification == outcome.Passed && t.Checked() {
				r.Verified++
			}

		}
		if t.State == plan.TaskDone && t.Verification == outcome.Unverified {
			r.Unverified++
		}
		switch t.Execution {
		case outcome.Failed:
			r.Failed++
		case outcome.Stopped:
			r.Stopped++
		case outcome.Cancelled:
			r.Cancelled++
		}
		if t.Verification == outcome.Rejected {
			r.Failed++
		}
		switch t.State {
		case plan.TaskRunning:
			r.Step = t.Title
			r.Agents++
		}
	}
	if r.Step == "" {
		for _, t := range tasks {
			if t.State == plan.TaskQueued {
				r.Step = t.Title
				break
			}
		}
	}
	if r.Step == "" && len(tasks) > 0 {
		r.Step = "settled"
	}
	return r
}

// onWorkflowsLoaded swaps in fresh rows, keeping the selection on its
// approach id.
func (m *Model) onWorkflowsLoaded(msg wfLoadedMsg) {
	if msg.err != nil {
		m.Wf.Err = truncate(msg.err.Error(), 120)
		return
	}
	m.Wf.Err = ""
	keep := ""
	if m.Wf.Sel < len(m.Wf.Rows) {
		keep = m.Wf.Rows[m.Wf.Sel].Approach
	}
	m.Wf.Rows = msg.rows
	m.Wf.Sel = 0
	for i, r := range msg.rows {
		if r.Approach == keep {
			m.Wf.Sel = i
		}
	}
	if m.Wf.Sel >= len(m.Wf.Rows) {
		m.Wf.Sel = 0
	}
}

// wfCur is the selected row, or nil on an empty floor.
func (m *Model) wfCur() *WfRow {
	if m.Wf.Sel < 0 || m.Wf.Sel >= len(m.Wf.Rows) {
		return nil
	}
	return &m.Wf.Rows[m.Wf.Sel]
}

// wfLaunchDeps preserves configured auth; the keypress never grants child tools.
func (m *Model) wfLaunchDeps(s *store.Store, confirms ...func(context.Context, string) bool) (launch.Deps, error) {
	return run.LaunchDeps(m.Cfg, s, m.Server, m.ModelName,
		m.Cfg.Agent.MaxSteps, m.St.CtxWindow, nil, io.Discard, confirms...)
}

// wfLaunch fans out the selected ready approach off the event loop.
// One run at a time: a second launch waits for the floor to clear.
func (m *Model) wfLaunch(r WfRow, resume bool) tea.Cmd {
	if m.servingPending() {
		return nil
	}
	if m.compactBusy(false) {
		m.alert("info", "compaction owns the model slot — wait or stop it first")
		return nil
	}
	if m.Chat.Running || m.Wf.Busy != "" {
		m.alert("info", "a workflow run is already in flight — wait for it to settle")
		return nil
	}
	path, pool := m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes)
	confirm := m.askConfirm(m.Chat.Seq, r.Approach, make(chan struct{}, 1))
	deps := func() (launch.Deps, *store.Store, error) {
		s, err := store.OpenWithBufferPool(path, pool)
		if err != nil {
			return launch.Deps{}, nil, err
		}
		deps, e := m.wfLaunchDeps(s, confirm)
		if e != nil {
			s.Close()
			return launch.Deps{}, nil, e
		}
		notify := deps.OnTaskSettled
		deps.OnTaskSettled = func(c context.Context, t *plan.Task) error {
			if notify != nil {
				if err := notify(c, t); err != nil {
					return err
				}
			}
			if m.Shared.Prog != nil {
				m.Shared.Prog.Send(wfTaskSettledMsg{ctx: c, approach: r.Approach, text: fmt.Sprintf("task %s attempt %d: execution=%s verification=%s qualified=%t", t.ID, t.Attempts, t.Execution, t.Verification, t.Qualified())})
			}
			return nil
		}
		return deps, s, nil
	}
	m.Wf.Busy = r.Approach
	ctx, cancel := context.WithCancel(context.Background())
	m.Wf.cancel, m.Wf.ctx = cancel, ctx
	if resume {
		return func() tea.Msg {
			d, s, err := deps()
			if err != nil {
				return wfDoneMsg{approach: r.Approach, ctx: ctx, err: err}
			}
			defer s.Close()
			if err = launch.ApproveLaunch(ctx, d, r.Approach); err != nil {
				return wfDoneMsg{approach: r.Approach, ctx: ctx, err: err}
			}
			results, err := launch.Resume(ctx, d, r.Approach)
			report, readErr := launch.WorkflowReport(d, r.Approach, err)
			return wfDoneMsg{approach: r.Approach, ctx: ctx, report: report, text: workflowCounts(results), note: d.BehaviorNote, err: errors.Join(err, readErr)}
		}
	}
	return func() tea.Msg {
		d, s, err := deps()
		if err != nil {
			return wfDoneMsg{approach: r.Approach, ctx: ctx, err: err}
		}
		defer s.Close()
		snapshot, err := launch.CheckReady(ctx, d, r.Approach)
		if err != nil {
			return wfDoneMsg{approach: r.Approach, ctx: ctx, note: d.BehaviorNote, err: err}
		}
		if err = launch.ApproveLaunch(ctx, d, r.Approach); err != nil {
			return wfDoneMsg{approach: r.Approach, ctx: ctx, err: err}
		}
		results, err := launch.LaunchChecked(ctx, d, r.Approach, snapshot)
		report, readErr := launch.WorkflowReport(d, r.Approach, err)
		return wfDoneMsg{approach: r.Approach, ctx: ctx, report: report, text: workflowCounts(results), note: d.BehaviorNote, err: errors.Join(err, readErr)}
	}
}

// onWfDone lands a finished run: loud on failure, refresh after.
func (m *Model) onWfDone(msg wfDoneMsg) tea.Cmd {
	if msg.ctx != nil && (msg.ctx != m.Wf.ctx || msg.approach != m.Wf.Busy) {
		return nil
	}
	if m.Chat.Dialog != nil && m.Chat.Dialog.Workflow == msg.approach {
		m.declineDialog()
	}
	m.Wf.Busy, m.Wf.ctx = "", nil
	if m.Wf.cancel != nil {
		m.Wf.cancel()
		m.Wf.cancel = nil
	}
	if m.quitPending {
		return m.requestQuit()
	}
	if msg.note != "" {
		msg.note += "\n"
	}
	if msg.err != nil {
		m.alert("err", msg.note+msg.err.Error())
		return m.refreshWorkflows()
	}
	level := "info"
	text := msg.text
	if msg.report != nil && !msg.report.Qualified {
		level = "err"
		text += "; " + string(msg.report.Outcome.Status) + ": " + msg.report.Outcome.Reason
	}
	m.alert(level, msg.note+fmt.Sprintf("workflow %s %s", msg.approach, text))
	return m.refreshWorkflows()
}

// wfPause stands the selected running approach down in the store; the
// next unscheduled task sees it. Synchronous — it only flips state.
func (m *Model) wfPause(r *WfRow) tea.Cmd {
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		m.alert("err", err.Error())
		return nil
	}
	defer s.Close()
	if err := launch.Pause(s, r.Approach); err != nil {
		m.alert("err", err.Error())
		return nil
	}
	m.alert("info", fmt.Sprintf("workflow %s paused — in-flight tasks finish, the rest wait", r.Approach))
	return m.refreshWorkflows()
}

// wfDive opens the plans tree with the approach selected, then the
// pending flag carries the second step: detail on arrival.
func (m *Model) wfDive(r *WfRow) tea.Cmd {
	m.Wf.Dive = r.Approach
	m.navigate(ViewPlans, "")
	return m.refreshPlans(r.Approach)
}

// workflowsKey drives the floor: arrows move, enter dives, l
// launches, p pauses or resumes, c toggles complete rows.
func (m Model) workflowsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyEnter:
		if r := m.wfCur(); !msg.Alt && r != nil {
			return m, m.wfDive(r), true
		}
	case tea.KeyUp:
		if len(m.Wf.Rows) > 0 {
			m.Wf.Sel = (m.Wf.Sel - 1 + len(m.Wf.Rows)) % len(m.Wf.Rows)
		}
		return m, nil, true
	case tea.KeyDown:
		if len(m.Wf.Rows) > 0 {
			m.Wf.Sel = (m.Wf.Sel + 1) % len(m.Wf.Rows)
		}
		return m, nil, true
	}
	switch msg.String() {
	case "l":
		if r := m.wfCur(); r != nil {
			if r.State != plan.ApproachReady {
				m.alert("info", fmt.Sprintf("workflow is %s — launch needs ready", r.State))
				return m, nil, true
			}
			return m, m.wfLaunch(*r, false), true
		}
	case "p":
		if r := m.wfCur(); r != nil {
			switch r.State {
			case plan.ApproachRunning:
				return m, m.wfPause(r), true
			case plan.ApproachPaused:
				return m, m.wfLaunch(*r, true), true
			default:
				m.alert("info", fmt.Sprintf("workflow is %s — pause needs running, resume needs paused", r.State))
				return m, nil, true
			}
		}
	case "c":
		m.Wf.ShowDone = !m.Wf.ShowDone
		return m, m.refreshWorkflows(), true
	}
	return m, nil, false
}

func (m Model) renderWorkflows(w, vh int) string {
	inner := BoxInner(w)
	done := "hidden"
	if m.Wf.ShowDone {
		done = "shown"
	}
	hints := Wrap(m.ownedHint("enter dive · l launch · p pause/resume · c complete:"+done+" · "+m.escapeHint()), inner)
	title := itemTitle("Workflows", m.Wf.Sel, len(m.Wf.Rows))
	if m.Wf.Busy != "" {
		title += " · run in flight"
	}
	var body []string
	if m.Wf.Err != "" {
		for _, l := range Wrap(m.Wf.Err, inner) {
			body = append(body, ErrSt.Render(l))
		}
	} else if len(m.Wf.Rows) == 0 {
		body = append(body, Muted.Render("no approaches yet — build one with the workflow_* tools or graph"))
	} else {
		namew := max(12, min(36, inner-48))
		body = append(body, Muted.Render(fmt.Sprintf("  %-*s %-12s %s", namew, "Approach", "State", "Qualified / Attempts")))
		var rows []string
		for i, r := range m.Wf.Rows {
			mark := "  "
			if i == m.Wf.Sel {
				mark = "❯ "
			}
			label := fmt.Sprintf("%s%-*s %-12s %d/%d qualified · %d attempts", mark, namew, truncate(r.Title, namew), truncate(r.State, 12), r.Done, r.Total, r.Attempts)
			if i == m.Wf.Sel {
				rows = append(rows, selectionRow(label, inner))
			} else {
				rows = append(rows, Body.Render(truncate(label, inner)))
			}
		}
		sel := min(max(0, m.Wf.Sel), len(m.Wf.Rows)-1)
		r := m.Wf.Rows[sel]
		parts := []string{InfoSt.Render(fmt.Sprintf("%d agents", r.Agents))}
		for _, count := range []struct {
			n       int
			label   string
			failure bool
		}{{r.Unverified, "unverified", false}, {r.Failed, "failed", true}, {r.Stopped, "stopped", false}, {r.Cancelled, "cancelled", false}} {
			if count.n == 0 {
				continue
			}
			style := WarnSt.Bold(false)
			if count.failure {
				style = ErrSt.Bold(false)
			}
			parts = append(parts, style.Render(fmt.Sprintf("%d %s", count.n, count.label)))
		}
		var details []string
		for _, l := range Wrap(strings.Join(parts, dot()), inner) {
			details = append(details, l)
		}
		for _, l := range Wrap("▸ "+r.Step, inner) {
			details = append(details, Body.Render(l))
		}
		detailBudget := min(len(details), max(3, vh-2-len(hints)-2))
		if len(details) > detailBudget {
			details = append(details[:max(1, detailBudget-1)], Muted.Render("… enter dive opens full approach details"))
		}
		room := max(1, vh-2-len(hints)-1-len(details))
		body = append(body, listWindow(rows, sel, room)...)
		body = append(body, details...)

	}
	return panel(title, body, hints, w, vh)
}

func workflowCounts(results []launch.Result) string {
	qualified, unverified, failed, stopped, cancelled := 0, 0, 0, 0, 0
	for _, r := range results {
		if r.Qualified() {
			qualified++
		}
		if r.State == plan.TaskDone && r.Verification == outcome.Unverified {
			unverified++
		}
		if r.Outcome.Status == outcome.Failed || r.Verification == outcome.Rejected {
			failed++
		}
		if r.Outcome.Status == outcome.Stopped {
			stopped++
		}
		if r.Outcome.Status == outcome.Cancelled {
			cancelled++
		}
	}
	return fmt.Sprintf("%d/%d qualified; %d unverified, %d failed, %d stopped, %d cancelled", qualified, len(results), unverified, failed, stopped, cancelled)
}

type wfTaskSettledMsg struct {
	ctx            context.Context
	approach, text string
}
