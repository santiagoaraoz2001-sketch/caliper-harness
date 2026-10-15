package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// Row kinds in the plan tree.
const (
	rowPlan     = "plan"
	rowApproach = "approach"
	rowTask     = "task"
)

// LoadedPlan is one plan with its approaches; refs P1.. run by
// creation order so older refs never shift.
type LoadedPlan struct {
	P          *plan.Plan
	Ref        string
	Approaches []LoadedApproach
}

// LoadedApproach is one approach with its tasks; refs P1-A1.. run by
// creation order within the plan, qualified by its reference.
type LoadedApproach struct {
	A     *plan.Approach
	Ref   string
	Tasks []*plan.Task
}

// PlanRow is one visible tree row.
type PlanRow struct {
	Depth                   int
	Kind                    string
	ID                      string
	Ref                     string
	Label                   string
	State                   string
	Done                    int
	Total                   int
	Tries                   int
	Execution, Verification string
	PI                      int
	AI                      int
	TI                      int
}

// PlansState is the plan tree: loaded plans, visible rows, selection.
type PlansState struct {
	Plans     []LoadedPlan
	Rows      []PlanRow
	Nodes     []PlanRow
	Request   uint64
	Sel       int
	Detail    *PlanDetail
	Scroll    int
	Collapsed map[string]bool
	Err       string
	Loaded    bool
}

// PlanDetail is one open dive: plan, approach, or task with its ref.
type PlanDetail struct {
	Kind string
	ID   string
	Ref  string
	LP   *LoadedPlan
	LA   *LoadedApproach
	T    *plan.Task
}

// plansLoadedMsg carries a store refresh into the loop.
type plansLoadedMsg struct {
	plans       []LoadedPlan
	target      string
	err         error
	request     uint64
	owner, path string
}

// loadPlans reads creation history; retired nodes reserve their references.
// Failure returns err; the caller keeps stale rows.
func loadPlans(storePath string, bufferPoolBytes uint64) ([]LoadedPlan, error) {
	s, err := store.OpenWithBufferPool(storePath, bufferPoolBytes)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	plans, err := plan.ListPlans(s, "")
	if err != nil {
		return nil, err
	}
	out := make([]LoadedPlan, 0, len(plans))
	for _, p := range plans {
		lp := LoadedPlan{P: p}
		appr, err := plan.PlanApproaches(s, p.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range appr {
			la := LoadedApproach{A: a}
			tasks, err := plan.ApproachTasks(s, a.ID)
			if err != nil {
				return nil, err
			}
			la.Tasks = tasks
			lp.Approaches = append(lp.Approaches, la)
		}
		out = append(out, lp)
	}
	return out, nil
}

// taskRef extends a qualified approach reference with its task ordinal.
func taskRef(aref string, ti int) string {
	return fmt.Sprintf("%s-T%d", aref, ti+1)
}

// isPlanRef distinguishes ordinal references from ordinary file paths.
func isPlanRef(ref string) bool {
	parts := strings.Split(ref, "-")
	if len(parts) > 3 || len(parts[0]) < 2 || !strings.ContainsRune("PA", rune(parts[0][0])) {
		return false
	}
	for i, part := range parts {
		if len(part) < 2 || (i > 0 && !strings.ContainsRune("AT", rune(part[0]))) {
			return false
		}
		n, err := strconv.Atoi(part[1:])
		if err != nil || n < 1 {
			return false
		}
	}
	return true
}

// rebuild flattens visible rows, preserving collapse state and the
// selection by node ID. target (from /plans <ref|id>) wins.
func (st *PlansState) rebuild(target string) {
	if st.Collapsed == nil {
		st.Collapsed = map[string]bool{}
	}
	previous := ""
	if st.Sel >= 0 && st.Sel < len(st.Rows) {
		previous = st.Rows[st.Sel].ID
	}
	plan.ByCreated(st.Plans, func(lp LoadedPlan) (time.Time, string) { return lp.P.CreatedAt, lp.P.ID })
	for pi := range st.Plans {
		lp := &st.Plans[pi]
		lp.Ref = fmt.Sprintf("P%d", pi+1)
		plan.ByCreated(lp.Approaches, func(la LoadedApproach) (time.Time, string) { return la.A.CreatedAt, la.A.ID })
		for ai := range lp.Approaches {
			la := &lp.Approaches[ai]
			la.Ref = fmt.Sprintf("%s-A%d", lp.Ref, ai+1)
			plan.ByCreated(la.Tasks, func(t *plan.Task) (time.Time, string) { return t.CreatedAt, t.ID })
		}
	}
	var rows []PlanRow
	for pi, lp := range st.Plans {
		done, total := 0, 0
		for _, la := range lp.Approaches {
			if la.A.State == plan.ApproachRetired {
				continue
			}
			for _, t := range la.Tasks {
				total++
				if t.Qualified() {
					done++
				}
			}
		}
		rows = append(rows, PlanRow{Depth: 0, Kind: rowPlan, ID: lp.P.ID, Ref: lp.Ref,
			Label: lp.P.Title, State: lp.P.State, Done: done, Total: total, PI: pi, AI: -1, TI: -1})
		for ai, la := range lp.Approaches {
			done, total := 0, len(la.Tasks)
			for _, t := range la.Tasks {
				if t.Qualified() {
					done++
				}
			}
			rows = append(rows, PlanRow{Depth: 1, Kind: rowApproach, ID: la.A.ID, Ref: la.Ref,
				Label: la.A.Title, State: la.A.State, Done: done, Total: total, PI: pi, AI: ai, TI: -1})
			for ti, t := range la.Tasks {
				rows = append(rows, PlanRow{Depth: 2, Kind: rowTask, ID: t.ID, Ref: taskRef(la.Ref, ti),
					Label: t.Title, State: t.State, Tries: t.Attempts, Execution: string(t.Execution), Verification: string(t.Verification), PI: pi, AI: ai, TI: ti})
			}
		}
	}
	st.Nodes = rows
	st.Err = ""
	if target != "" {
		r, err := st.resolve(target)
		if err != nil {
			st.Err = err.Error()
		} else {
			delete(st.Collapsed, st.Plans[r.PI].P.ID)
			if r.AI >= 0 {
				delete(st.Collapsed, st.Plans[r.PI].Approaches[r.AI].A.ID)
			}
			target = r.ID
		}
	} else {
		target = previous
	}
	st.Rows, st.Sel = nil, -1
	for _, r := range rows {
		if st.retired(r) || (r.Depth > 0 && st.Collapsed[st.Plans[r.PI].P.ID]) || (r.Depth > 1 && st.Collapsed[st.Plans[r.PI].Approaches[r.AI].A.ID]) {
			continue
		}
		st.Rows = append(st.Rows, r)
		if st.Err == "" && r.ID == target {
			st.Sel = len(st.Rows) - 1
		}
	}
	if st.Sel < 0 && st.Err == "" && len(st.Rows) > 0 {
		st.Sel = 0
	}
}

func (st *PlansState) retired(r PlanRow) bool {
	return st.Plans[r.PI].P.State == plan.PlanRetired || (r.AI >= 0 && st.Plans[r.PI].Approaches[r.AI].A.State == plan.ApproachRetired)
}

// resolve includes hidden and retired nodes: an alias can never change owner.
func (st *PlansState) resolve(ref string) (*PlanRow, error) {
	nodes := make([]plan.Reference, len(st.Nodes))
	for i, r := range st.Nodes {
		nodes[i] = plan.Reference{Kind: r.Kind, ID: r.ID, Ref: r.Ref, Retired: st.retired(r)}
	}
	n, err := plan.ResolveReference(nodes, ref)
	if err != nil {
		return nil, err
	}
	for i := range st.Nodes {
		if st.Nodes[i].ID == n.ID {
			return &st.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("resolved node disappeared")
}

func (m *Model) plansActive() bool {
	return m.Tabs[m.Active].at.view == ViewPlans
}

// refreshPlans reloads the tree off the event loop.
func (m *Model) refreshPlans(target string) tea.Cmd {
	m.Plans.Request++
	request, owner, path := m.Plans.Request, m.Chat.ID, m.StorePath
	pool := uint64(m.Cfg.Store.BufferPoolBytes)
	return func() tea.Msg {
		lp, err := loadPlans(path, pool)
		return plansLoadedMsg{plans: lp, target: target, err: err, request: request, owner: owner, path: path}
	}
}

func (m *Model) onPlansLoaded(msg plansLoadedMsg) {
	if msg.request != m.Plans.Request || msg.owner != m.Chat.ID || msg.path != m.StorePath || !m.plansActive() {
		return
	}
	if msg.err != nil {
		m.Plans.Err = truncate(msg.err.Error(), 120)
		if msg.target != "" {
			m.Plans.Sel, m.Plans.Detail = -1, nil
			m.alert("err", m.Plans.Err)
		}
		return
	}
	refused := m.Plans.Err
	if !m.Plans.Loaded || msg.target != "" || m.plansCur() != nil || m.Plans.Detail != nil || m.Wf.Dive != "" {
		refused = ""
	}
	m.Plans.Err = ""
	m.Plans.Plans = msg.plans
	m.Plans.Loaded = true
	target := msg.target
	if target == "" {
		if r := m.plansCur(); r != nil {
			target = r.ID
		}
	}
	open := m.Plans.Detail != nil
	m.Plans.rebuild(target)
	if refused != "" {
		m.Plans.Sel, m.Plans.Err = -1, refused
	}
	m.Plans.Detail = nil
	if m.Plans.Err != "" && refused == "" {
		m.alert("err", m.Plans.Err)
	}
	if open && m.Plans.Err == "" {
		m.plansDetail(m.plansCur())
	}
	// Workflows dive, second step: the tree arrived with the approach
	// selected — open its detail now.
	if m.Wf.Dive != "" {
		id := m.Wf.Dive
		m.Wf.Dive = ""
		m.plansSelect(id)
		m.plansDetail(m.plansCur())
	}
}

func (m *Model) plansCur() *PlanRow {
	if m.Plans.Sel < 0 || m.Plans.Sel >= len(m.Plans.Rows) {
		return nil
	}
	return &m.Plans.Rows[m.Plans.Sel]
}

func (m *Model) plansSelect(id string) {
	for i, r := range m.Plans.Rows {
		if r.ID == id {
			m.Plans.Sel = i
			return
		}
	}
}

func (m *Model) plansMove(d int) {
	if len(m.Plans.Rows) == 0 {
		return
	}
	m.Plans.Sel += d
	if m.Plans.Sel < 0 {
		m.Plans.Sel = 0
	}
	if m.Plans.Sel >= len(m.Plans.Rows) {
		m.Plans.Sel = len(m.Plans.Rows) - 1
	}
}

func (m *Model) plansToggle(r *PlanRow, open bool) {
	if r == nil || r.Kind == rowTask {
		return
	}
	if open {
		delete(m.Plans.Collapsed, r.ID)
	} else {
		m.Plans.Collapsed[r.ID] = true
	}
	m.Plans.rebuild("")
}

// plansDetail opens the deep dive for a task row.
// plansDetail dives into any row: plan, approach, or task.
func (m *Model) plansDetail(r *PlanRow) {
	if r == nil {
		return
	}
	switch r.Kind {
	case rowTask:
		t := m.Plans.Plans[r.PI].Approaches[r.AI].Tasks[r.TI]
		m.Plans.Detail = &PlanDetail{Kind: rowTask, ID: r.ID, Ref: r.Ref, T: t}
	case rowApproach:
		la := m.Plans.Plans[r.PI].Approaches[r.AI]
		m.Plans.Detail = &PlanDetail{Kind: rowApproach, ID: r.ID, Ref: r.Ref, LA: &la}
	case rowPlan:
		lp := m.Plans.Plans[r.PI]
		m.Plans.Detail = &PlanDetail{Kind: rowPlan, ID: r.ID, Ref: r.Ref, LP: &lp}
	}
	m.Plans.Scroll = 0
}

// detailAvail is the task-body window for height h: header, state
// line, and box frame all come out of the budget first.
func detailAvail(h int) int { return max(0, h-4) }

func (m *Model) plansScroll(d, h int) {
	if m.Plans.Detail != nil {
		m.Plans.Scroll = layoutLines(detailLogical(m.Plans.Detail), BoxInner(m.Width)).clamp(m.Plans.Scroll+d, detailAvail(m.plansHeight(h, m.Width)))
	}
}

// detailLogical is the dive content as numbered logical lines: title,
// state, then kind-specific body. Wrapping happens in display so
// line numbers stay stable.
func detailLogical(d *PlanDetail) []string {
	switch d.Kind {
	case rowPlan:
		lp := d.LP
		done, total := 0, 0
		for _, la := range lp.Approaches {
			if la.A.State == plan.ApproachRetired {
				continue
			}
			for _, t := range la.Tasks {
				total++
				if t.Qualified() {
					done++
				}
			}
		}
		out := []string{d.Ref + " " + lp.P.Title, "state: " + lp.P.State,
			"goal: " + lp.P.Goal, fmt.Sprintf("progress: %d/%d tasks qualified", done, total),
			"", "approaches:"}
		for _, la := range lp.Approaches {
			ad, at := 0, len(la.Tasks)
			for _, t := range la.Tasks {
				if t.Qualified() {
					ad++
				}
			}
			out = append(out, fmt.Sprintf("%s %s · %s · %d/%d", la.Ref, la.A.Title, la.A.State, ad, at))
		}
		return out
	case rowApproach:
		la := d.LA
		done, total := 0, len(la.Tasks)
		for _, t := range la.Tasks {
			if t.Qualified() {
				done++
			}
		}
		out := []string{d.Ref + " " + la.A.Title, "state: " + la.A.State,
			fmt.Sprintf("progress: %d/%d tasks qualified", done, total), "", "tasks:"}
		for ti, t := range la.Tasks {
			out = append(out, fmt.Sprintf("%s %s · %s", taskRef(la.Ref, ti), t.Title, t.State))
		}
		return out
	default:
		t := d.T
		out := []string{d.Ref + " " + t.Title, "state: " + t.State + " · execution=" + string(t.Execution) + " verification=" + string(t.Verification) + " qualified=" + fmt.Sprint(t.Qualified()), ""}
		fields := []struct{ name, val string }{
			{"prompt", t.Prompt},
			{"goal", t.Current},
			{"target", t.Target},
			{"sketch", t.Sketch},
			{"accept", t.Acceptance},
			{"tests", t.Tests},
			{"verify", t.Verify},
			{"project check command", t.CheckCommand},
			{"acceptance command", t.AcceptanceCommand},
			{"result", t.Result},
			{"execution reason", t.ExecutionReason},
			{"verification reason", t.VerificationReason},
			{"review", t.Review},
			{"attempt log", t.AttemptLog},
		}
		for _, f := range fields {
			if strings.TrimSpace(f.val) == "" {
				continue
			}
			out = append(out, f.name+":")
			out = append(out, strings.Split(f.val, "\n")...)
		}
		if len(out) == 3 {
			out = append(out, "(undrafted — no block fields yet)")
		}
		return out
	}
}

// detailDisplay numbers the logical lines and wraps for width.
// detailDisplay numbers the dive's logical lines: shared numbering,
// plan-supplied content.
func detailDisplay(d *PlanDetail, inner int) []string {
	return Numbered(detailLogical(d), inner)
}

// detailQuote extracts the quoted logical lines for a [spec].
func detailQuote(d *PlanDetail, spec string) (string, error) {
	return Quote(detailLogical(d), spec)
}

// plansKey drives the tree. done=false falls through to input.
func (m Model) plansKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	open := m.Plans.Detail != nil
	switch msg.Type {
	case tea.KeyUp:
		if open {
			m.plansScroll(-1, viewHeight(m))
		} else {
			m.plansMove(-1)
		}
		return m, nil, true
	case tea.KeyDown:
		if open {
			m.plansScroll(1, viewHeight(m))
		} else {
			m.plansMove(1)
		}
		return m, nil, true
	case tea.KeyPgUp, tea.KeyPgDown:
		if open {
			d := detailAvail(m.plansHeight(viewHeight(m), m.Width))
			if msg.Type == tea.KeyPgUp {
				m.plansScroll(-d, viewHeight(m))
			} else {
				m.plansScroll(d, viewHeight(m))
			}
		}
		return m, nil, true
	case tea.KeyRight:
		if r := m.plansCur(); r != nil {
			if r.Kind == rowTask {
				m.plansDetail(r)
			} else {
				m.plansToggle(r, true)
			}
		}
		return m, nil, true
	case tea.KeyLeft:
		if r := m.plansCur(); r != nil {
			if r.Kind == rowTask && r.AI >= 0 {
				aid := m.Plans.Plans[r.PI].Approaches[r.AI].A.ID
				m.Plans.Collapsed[aid] = true
				m.Plans.rebuild("")
				m.plansSelect(aid)
			} else {
				m.plansToggle(r, false)
			}
		}
		return m, nil, true
	case tea.KeyEnter:
		if strings.TrimSpace(m.Input.Value()) != "" {
			return m, nil, false
		}
		if open {
			return m, nil, true
		}
		if r := m.plansCur(); r != nil {
			m.plansDetail(r)
		}
		return m, nil, true
	case tea.KeyRunes:
		s := msg.String()
		if (s == "r" || s == "R") && strings.TrimSpace(m.Input.Value()) == "" {
			return m, m.refreshPlans(""), true
		}
		if (s == "f" || s == "F") && strings.TrimSpace(m.Input.Value()) == "" {
			if r := m.plansCur(); r != nil {
				if m.Plans.Detail == nil || m.Plans.Detail.ID != r.ID {
					m.plansDetail(r)
				}
				m.Input.SetValue("/comment " + r.Ref + " ")
				m.Input.SetCursor(len(m.Input.Value()))
				m.PickerHide = true
				m.PickerIdx = 0
			}
			return m, nil, true
		}
		return m, nil, false
	}
	return m, nil, false
}

// planBar renders a 5-cell progress bar.
func planBar(done, total int) string {
	f := 0
	if total > 0 {
		f = done * 5 / total
	}
	if f < 0 {
		f = 0
	}
	if f > 5 {
		f = 5
	}
	return strings.Repeat("▓", f) + strings.Repeat("░", 5-f)
}

func (m *Model) planRowText(r PlanRow, sel bool, width int, last bool) string {
	mark := "  "
	if sel {
		mark = "❯ "
	}
	glyph := "  "
	if r.Kind != rowTask {
		glyph = "▾ "
		if m.Plans.Collapsed[r.ID] {
			glyph = "▸ "
		}
	} else if last {
		glyph = "└─ "
	} else {
		glyph = "├─ "
	}
	head := mark + strings.Repeat("  ", r.Depth) + glyph + r.Ref + " " + r.Label
	var tail string
	if r.Kind == rowTask {
		tail = r.State
		if r.Execution != "" {
			tail += " · " + r.Execution + "/" + r.Verification
		}
		if r.Tries > 1 {
			tail += fmt.Sprintf(" · ×%d", r.Tries)
		}
	} else {
		tail = fmt.Sprintf("%s %s %d/%d", r.State, planBar(r.Done, r.Total), r.Done, r.Total)
	}
	head = truncate(head, width-len([]rune(tail))-3)
	if r.Kind == rowTask && r.State == plan.TaskFailed {
		return head + " · " + ErrSt.Render(tail)
	}
	if strings.HasPrefix(tail, plan.TaskRunning) {
		return head + " · " + InfoSt.Render(tail)
	}
	return head + " · " + tail
}

// renderPlans draws header plus the tree in exactly h rows.
func (m Model) plansHintRows(w int) []string {
	text := "↑↓ move · →← expand · enter dive · f comment · r refresh"
	if m.Plans.Detail != nil {
		text = "↑↓/pgup/pgdn scroll · f comment [lines]"
	}
	text = m.ownedHint(text + " · " + m.escapeHint())
	var out []string
	for _, l := range Wrap(text, max(1, w-4)) {
		out = append(out, hintRule(l, w))
	}
	return out
}
func (m Model) plansHeight(h, w int) int {
	n := h - len(m.plansHintRows(w))
	if m.Plans.Err != "" {
		n--
	}
	return n
}

func (m *Model) renderPlans(w, h int) string {
	done, total, active := 0, 0, 0
	for _, lp := range m.Plans.Plans {
		if lp.P.State == plan.PlanRetired {
			continue
		}
		active++
		for _, la := range lp.Approaches {
			if la.A.State == plan.ApproachRetired {
				continue
			}
			for _, t := range la.Tasks {
				total++
				if t.Qualified() {
					done++
				}
			}
		}
	}
	left := fmt.Sprintf("Plans · %d active · %d/%d tasks qualified", active, done, total)
	if n := len(m.Comment); n > 0 {
		left += fmt.Sprintf(" · ◈%d comments", n)
	}
	rows := []string{Title.Render(truncate(left, w))}
	rows = append(rows, m.plansHintRows(w)...)
	contentHeight := m.plansHeight(h, w)
	if !m.Plans.Loaded {
		rows = append(rows, Muted.Render("loading…"))
	} else if m.Plans.Detail != nil {
		rows = append(rows, m.detailLines(w, contentHeight)...)
	} else {
		rows = append(rows, m.treeLines(w, contentHeight)...)
	}
	if m.Plans.Err != "" {
		rows = append(rows, WarnSt.Render("! "+truncate(m.Plans.Err, w)))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return strings.Join(rows[:h], "\n")
}

func (m *Model) treeLines(w, h int) []string {
	budget := h - 1
	if budget < 1 {
		return nil
	}
	if len(m.Plans.Rows) == 0 {
		return []string{Muted.Render("No active plans — create one with the graph mutations.")}
	}
	n := budget
	if n < 1 {
		n = 1
	}
	start := m.Plans.Sel - n + 1
	if start < 0 {
		start = 0
	}
	end := start + n
	if end > len(m.Plans.Rows) {
		end = len(m.Plans.Rows)
	}
	var rows []string

	for i := start; i < end; i++ {
		r := m.Plans.Rows[i]
		last := r.Kind == rowTask && (i+1 >= len(m.Plans.Rows) || m.Plans.Rows[i+1].Kind != rowTask)
		rows = append(rows, m.planRowText(r, i == m.Plans.Sel, w, last))
	}
	if end < len(m.Plans.Rows) {
		rows = append(rows, Muted.Render(fmt.Sprintf("…%d later", len(m.Plans.Rows)-end)))
	}
	if len(rows) > budget {
		rows = rows[:budget]
	}
	return rows
}

func (m *Model) detailLines(w, h int) []string {
	budget := h - 1
	if budget < 1 || m.Plans.Detail == nil {
		return nil
	}
	inner := BoxInner(w)
	body := detailDisplay(m.Plans.Detail, inner)
	avail := detailAvail(h)
	scroll := layoutLines(detailLogical(m.Plans.Detail), inner).clamp(m.Plans.Scroll, avail)
	var content []string
	for i := 0; i < avail; i++ {
		if scroll+i < len(body) {
			content = append(content, body[scroll+i])
		} else {
			content = append(content, "")
		}
	}
	box := Box(m.Plans.Detail.Ref, strings.Join(content, "\n"), w)
	rows := strings.Split(box, "\n")
	if len(rows) > budget {
		rows = rows[:budget]
	}
	return rows
}
