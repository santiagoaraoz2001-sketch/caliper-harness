package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/tools"
	"specific-harness/internal/workflowlib"
)

// LibState is the reusable workflow library view: published and draft
// blocks/workflows plus legacy pinned executions from the shared library file.
type LibState struct {
	Blocks     []workflowlib.Block
	Workflows  []workflowlib.Workflow
	Executions []workflowlib.Execution
	Sel        int
	Err        string
	Loaded     bool
}

func (m *Model) libraryPath() string { return tools.LibraryPath(".") }

func (m *Model) refreshLibrary() tea.Cmd {
	s, err := workflowlib.Open(m.libraryPath())
	if err != nil {
		m.Lib.Err = err.Error()
		return nil
	}
	if err := workflowlib.EnsureSeed(s); err != nil {
		m.Lib.Err = err.Error()
		return nil
	}
	if err := workflowlib.EnsureSeedPack(s); err != nil {
		m.Lib.Err = err.Error()
		return nil
	}
	m.Lib.Blocks, m.Lib.Workflows = s.List()
	m.Lib.Executions = s.ListExecutions()
	m.Lib.Err, m.Lib.Loaded = "", true
	if m.Lib.Sel >= m.libRows() {
		m.Lib.Sel = max(0, m.libRows()-1)
	}
	return nil
}

func (m *Model) libRows() int {
	return len(m.Lib.Workflows) + len(m.Lib.Blocks) + len(m.Lib.Executions)
}

// libSelection describes the selected row as kind, label, and index.
func (m *Model) libSelection() (kind string, idx int) {
	sel := m.Lib.Sel
	if sel < len(m.Lib.Workflows) {
		return "workflow", sel
	}
	sel -= len(m.Lib.Workflows)
	if sel < len(m.Lib.Blocks) {
		return "block", sel
	}
	sel -= len(m.Lib.Blocks)
	if sel < len(m.Lib.Executions) {
		return "execution", sel
	}
	return "", -1
}

func (m Model) libraryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if (msg.Type == tea.KeyEnter || msg.Type == tea.KeyRunes) && strings.TrimSpace(m.Input.Value()) != "" {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyUp:
		if n := m.libRows(); n > 0 {
			m.Lib.Sel = (m.Lib.Sel - 1 + n) % n
		}
		return m, nil, true
	case tea.KeyDown:
		if n := m.libRows(); n > 0 {
			m.Lib.Sel = (m.Lib.Sel + 1) % n
		}
		return m, nil, true
	}
	switch msg.String() {
	case "r":
		return m, m.refreshLibrary(), true
	case "a":
		return m, m.libToggleActivation(), true
	case "s":
		return m, m.libAttach(), true
	case "e":
		return m, m.libElaborate(), true
	case "b":
		m.navigate(ViewChat, "")
		m.Input.SetValue("Build a reusable skill workflow with me. First read the library for a similar workflow and tell me whether to use, fork, revise, or create one. Then draft it with build_block/build_workflow, show me the result, and publish/enable only as we agree.")
		m.Input.CursorEnd()
		return m, nil, true
	}
	return m, nil, false
}

func (m *Model) libToggleActivation() tea.Cmd {
	kind, idx := m.libSelection()
	if kind != "workflow" {
		m.alert("info", "activation applies to a workflow row")
		return nil
	}
	w := m.Lib.Workflows[idx]
	if w.Status != workflowlib.StatusPublished {
		m.alert("info", fmt.Sprintf("workflow %s@%d is %s — activation needs published", w.ID, w.Version, w.Status))
		return nil
	}
	s, err := workflowlib.Open(m.libraryPath())
	if err != nil {
		m.alert("err", err.Error())
		return nil
	}
	act, _ := s.Activation(w.ID)
	enable := !(act.Enabled && act.Version == w.Version)
	if err := s.SetActivation(w.ID, w.Version, enable); err != nil {
		m.alert("err", err.Error())
		return nil
	}
	state := "disabled"
	if enable {
		state = "enabled"
	}
	m.alert("info", fmt.Sprintf("workflow %s@%d %s", w.ID, w.Version, state))
	return m.refreshLibrary()
}

func (m *Model) libAttach() tea.Cmd {
	kind, idx := m.libSelection()
	if kind != "workflow" {
		m.alert("info", "attach applies to an enabled workflow row")
		return nil
	}
	w := m.Lib.Workflows[idx]
	s, err := workflowlib.Open(m.libraryPath())
	if err != nil {
		m.alert("err", err.Error())
		return nil
	}
	act, ok := s.Activation(w.ID)
	if w.Status != workflowlib.StatusPublished || !ok || !act.Enabled || act.Version != w.Version {
		m.alert("info", fmt.Sprintf("workflow %s@%d must be the enabled published version before it can attach", w.ID, w.Version))
		return nil
	}
	m.navigate(ViewChat, "")
	m.Input.SetValue(fmt.Sprintf("Attach workflow %s@%d as a method lens for the current major work. Use it inside PAT or coding with ordinary visible tools; PAT acceptance, the configured check, and independent review remain authoritative.", w.ID, w.Version))
	m.Input.CursorEnd()
	return nil
}

func (m *Model) libElaborate() tea.Cmd {
	kind, idx := m.libSelection()
	var subject string
	switch kind {
	case "workflow":
		w := m.Lib.Workflows[idx]
		subject = fmt.Sprintf("library workflow %s@%d (%s)", w.ID, w.Version, w.Title)
	case "block":
		b := m.Lib.Blocks[idx]
		subject = fmt.Sprintf("library block %s@%d (%s)", b.ID, b.Version, b.Title)
	case "execution":
		e := m.Lib.Executions[idx]
		subject = fmt.Sprintf("library execution %s of %s@%d", e.ID, e.WorkflowID, e.WorkflowVersion)
	default:
		m.alert("info", "nothing selected to elaborate")
		return nil
	}
	m.navigate(ViewChat, "")
	m.Input.SetValue(fmt.Sprintf("Elaborate %s: explain its purpose, inputs, steps, completion evidence, failure route, and what is pinned. Use library_workflow to read current state. Do not change anything unless I ask.", subject))
	m.Input.CursorEnd()
	return nil
}

func (m *Model) renderLibrary(w, vh int) string {
	inner := BoxInner(w)
	hints := Wrap(m.ownedHint("up/down select · a enable/disable workflow · s attach method lens · e elaborate · b build with agent · r refresh · "+m.escapeHint()), inner)
	title := itemTitle("Workflow library", m.Lib.Sel, m.libRows())
	var body []string
	if m.Lib.Err != "" {
		for _, l := range Wrap(m.Lib.Err, inner) {
			body = append(body, ErrSt.Render(l))
		}
	} else if m.libRows() == 0 {
		body = append(body, Muted.Render("library is empty — b builds a reusable workflow with the agent"))
	} else {
		row := 0
		add := func(label string) {
			mark := "  "
			if row == m.Lib.Sel {
				mark = "❯ "
				body = append(body, selectionRow(truncate(mark+label, inner), inner))
			} else {
				body = append(body, Body.Render(truncate(mark+label, inner)))
			}
			row++
		}
		for _, wf := range m.Lib.Workflows {
			steps := make([]string, len(wf.Steps))
			for i, st := range wf.Steps {
				steps[i] = fmt.Sprintf("%s@%d", st.BlockID, st.BlockVersion)
			}
			add(fmt.Sprintf("workflow %s@%d %-10s %s · steps: %s", wf.ID, wf.Version, wf.Status, wf.Title, strings.Join(steps, " → ")))
		}
		for _, b := range m.Lib.Blocks {
			add(fmt.Sprintf("block    %s@%d %-10s %s · %s", b.ID, b.Version, b.Status, b.Title, b.Implementation.Type))
		}
		for _, e := range m.Lib.Executions {
			add(fmt.Sprintf("legacy   %s %-10s %s@%d · step %d/%d", e.ID, e.Status, e.WorkflowID, e.WorkflowVersion, e.Current, len(e.Steps)))
		}
	}
	return panel(title, body, hints, w, vh)
}
