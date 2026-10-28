package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/config"
	"specific-harness/internal/graph"
	"specific-harness/internal/run"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tools/files"
)

// TestsState is a project inspector and a separate command owner. Test receipts
// never become the current conversation's or workflow's verification state.
type TestsState struct {
	Sel, Scroll   int
	Editing       bool
	EditID        string
	Detail        bool
	Busy          string
	Stopping      bool
	ctx           context.Context
	cancel        context.CancelFunc
	loadCancel    context.CancelFunc
	History       []run.TestRun
	Specs         map[string]testDesignSpec
	ContextConfig string
	Freshness     map[string]string
	Readiness     map[string]string
	Ticket        int
	Err           string
}

type testsHistoryMsg struct {
	ticket        int
	runs          []run.TestRun
	fresh         map[string]string
	ready         map[string]string
	specs         map[string]testDesignSpec
	contextConfig string
	err           error
}
type testsDoneMsg struct {
	owner  string
	ctx    context.Context
	result run.TestRun
	err    error
}

func (m Model) testsActive() bool { return m.Tabs[m.Active].at.view == ViewTests }
func (m Model) testRows() []config.TestSuite {
	rows := run.TestSuites(m.Cfg)
	if strings.TrimSpace(m.Cfg.Coding.CheckCommand) == "" {
		rows = append([]config.TestSuite{{ID: run.ConfiguredCheckID, Name: "Coding check unconfigured", Purpose: "No project coding command configured", Expected: "Configure the project command in /settings", Required: true, Enabled: true}}, rows...)
	}
	return rows
}
func (m Model) selectedTest() config.TestSuite {
	rows := m.testRows()
	return rows[min(max(0, m.Tests.Sel), len(rows)-1)]
}
func (m Model) testMutationBusy() bool {
	return m.modelWorkActive() || m.Chat.Dialog != nil || m.Serve.Loading != "" || m.Settings.Applying || m.Settings.DecisionDraft != nil
}
func (m *Model) saveTesting(next config.TestingConfig) bool {
	if m.testMutationBusy() {
		m.alert("info", "wait for active work to settle before changing tests")
		return false
	}
	cfg := m.Cfg
	cfg.Testing = next
	path := m.CfgPath
	if path == "" {
		path = config.DefaultFile()
	}
	if err := config.SaveTesting(path, cfg); err != nil {
		m.alert("err", err.Error())
		return false
	}
	m.Cfg.Testing = next
	m.CfgPath = path
	m.Tests.Ticket++
	if m.Tests.loadCancel != nil {
		m.Tests.loadCancel()
		m.Tests.loadCancel = nil
	}
	m.Tests.Freshness = nil // Historical evidence needs comparison with the new selection.
	m.Tests.Readiness = nil
	m.Tests.Specs = nil
	m.alert("info", "project test configuration saved; previous results remain archived")
	return true
}
func (m *Model) toggleTest() {
	s := m.selectedTest()
	if s.Required {
		m.alert("info", "the configured coding check is required; it cannot be disabled here")
		return
	}
	next := m.Cfg.Testing
	next.Suites = append([]config.TestSuite(nil), next.Suites...)
	for i := range next.Suites {
		if next.Suites[i].ID == s.ID {
			next.Suites[i].Enabled = !s.Enabled
			next.Suites[i].DisabledReason = ""
			if s.Enabled {
				next.Suites[i].DisabledReason = "Disabled by the user in /tests"
			}
		}
	}
	m.saveTesting(next)
}
func (m *Model) editTest(add bool) {
	if m.testMutationBusy() {
		m.alert("info", "edit tests when idle")
		return
	}
	s := m.selectedTest()
	if !add && s.Required {
		m.alert("info", "edit the configured coding check in /settings; its required status is fixed")
		return
	}
	m.saveTabPresentation()
	m.Tests.Editing, m.Tests.EditID = true, s.ID
	if add {
		m.Tests.EditID = ""
		s = config.TestSuite{ID: "new-suite", Name: "New suite", Purpose: "Describe the behavior under test", Expected: "Describe the observable expected result", Command: "", Enabled: true}
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	m.Input.SetValue(string(b))
	m.Input.SetCursor(len(m.Input.Value()))
	m.PickerHide = true
	m.alert("info", "edit suite JSON; Enter saves, Ctrl+J adds a line, Escape discards")
}
func (m *Model) cancelTestEdit() {
	m.Tests.Editing, m.Tests.EditID = false, ""
	m.testsEditor = nil
	m.restoreTabEditor()
}
func (m *Model) applyTestEdit(raw string) {
	m.Input.SetValue(raw)
	m.Input.SetCursor(len(raw))
	m.PickerHide = true
	var s config.TestSuite
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		m.alert("err", "test suite JSON: "+err.Error())
		return
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		m.alert("err", "test suite JSON must contain exactly one object")
		return
	}
	if m.Tests.EditID != "" && s.ID != m.Tests.EditID {
		m.alert("err", "suite ID is stable; add a new suite instead of changing its ID")
		return
	}
	next := m.Cfg.Testing
	next.Suites = append([]config.TestSuite(nil), next.Suites...)
	if m.Tests.EditID == "" {
		next.Suites = append(next.Suites, s)
	} else {
		for i := range next.Suites {
			if next.Suites[i].ID == m.Tests.EditID {
				next.Suites[i] = s
			}
		}
	}
	if m.saveTesting(next) {
		m.cancelTestEdit()
	}
}

func (m *Model) designTest() {
	if m.testMutationBusy() {
		m.alert("info", "design tests when idle")
		return
	}
	s := m.selectedTest()
	m.navigate(ViewChat, "")
	// A design request is a visible draft, never an automatic model call. Do not
	// overwrite a user's existing draft to make room for generated instructions.
	if strings.TrimSpace(m.Input.Value()) != "" {
		found := false
		for i := range m.Drafts {
			if i != m.Draft && strings.TrimSpace(m.Drafts[i]) == "" {
				m.switchDraft(i)
				found = true
				break
			}
		}
		if !found {
			m.alert("info", "all drafts contain text; save or clear one before preparing test design")
			return
		}
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	seed := "Help me design or refine this project's test suite. State the behavior, expected observable result, a meaningful failing case, prerequisites, and a bounded command. Preserve existing required checks. Propose the suite definition and any test code changes; do not claim a test ran without current evidence.\n\nSelected suite:\n" + string(b)
	seed += m.testDesignContext(s)
	m.Input.SetValue(seed)
	m.Input.SetCursor(len(seed))
	m.PickerHide = true
	m.alert("info", "test design draft prepared; review and press Enter to send")
}

func (m *Model) refreshTests() tea.Cmd {
	if m.Tests.loadCancel != nil {
		m.Tests.loadCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	m.Tests.loadCancel = cancel
	m.Tests.Ticket++
	ticket, cfg, dir, path := m.Tests.Ticket, m.Cfg, m.SessionsPath, m.StorePath
	selected := m.selectedTest()
	cfg.Sessions.Path = dir
	return func() tea.Msg {
		defer cancel()
		w, err := files.Capture(cfg.Tools.Bash)
		if err != nil {
			return testsHistoryMsg{ticket: ticket, err: err}
		}
		runs, err := run.ListTestRuns(ctx, dir, w.Path, 20)
		msg := testsHistoryMsg{ticket: ticket, runs: runs, fresh: map[string]string{}, ready: map[string]string{}, err: err}
		s, err := store.OpenWithBufferPool(path, uint64(cfg.Store.BufferPoolBytes))
		if err != nil {
			msg.err = err
			return msg
		}
		defer s.Close()
		msg.specs = testDesignSpecs(ctx, cfg, s, w.Path, selected)
		msg.contextConfig = testDesignConfig(cfg)
		deps := run.TestDeps{Cfg: cfg, Workspace: w.Path, Store: s}
		for _, row := range run.TestSuites(cfg) {
			ok, reason := run.TestReadiness(ctx, deps, row)
			if ok {
				reason = "prerequisites satisfied at last refresh; authorization and runtime execution still required"
			}
			msg.ready[row.ID] = reason
		}
		fresh, freshErr := run.TestFreshnessBatch(ctx, deps, runs)
		if freshErr != nil {
			msg.err = freshErr
		}
		for i, r := range runs {
			f := "unknown"
			if i < len(fresh) {
				f = fresh[i]
			}
			if f == "" {
				f = "unknown"
			}
			msg.fresh[r.Selection.ID] = f
		}
		return msg
	}
}
func (m *Model) startTest() tea.Cmd {
	return m.startTestSelection(false)
}
func (m *Model) startTestSelection(all bool) tea.Cmd {
	if m.testMutationBusy() {
		m.alert("info", "another operation owns the workspace; wait for it to settle")
		return nil
	}
	suite := m.selectedTest()
	if !all && !suite.Enabled {
		m.alert("info", "enable this optional suite before running it")
		return nil
	}
	if !all && strings.TrimSpace(suite.Command) == "" {
		m.alert("info", "configure a check command in /settings first")
		return nil
	}
	ids := []string{suite.ID}
	if all {
		ids = nil
		for _, s := range m.Cfg.Testing.Suites {
			if s.Enabled {
				ids = append(ids, s.ID)
			}
		}
		if len(ids) == 0 {
			m.alert("info", "no enabled optional suites; r runs the selected configured check explicitly")
			return nil
		}
	}
	owner := "test-ui-" + session.NewID()
	if m.Tests.loadCancel != nil {
		m.Tests.loadCancel()
		m.Tests.loadCancel = nil
		m.Tests.Ticket++
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.Tests.Busy, m.Tests.ctx, m.Tests.cancel, m.Tests.Stopping = owner, ctx, cancel, false
	cfg, path, dir, version := m.Cfg, m.StorePath, m.SessionsPath, m.Version
	cfg.Sessions.Path = dir
	confirm := m.askTestConfirm(owner, ctx)
	return func() tea.Msg {
		msg := testsDoneMsg{owner: owner, ctx: ctx}
		w, err := files.Capture(cfg.Tools.Bash)
		if err != nil {
			msg.err = err
			return msg
		}
		s, err := store.OpenWithBufferPool(path, uint64(cfg.Store.BufferPoolBytes))
		if err != nil {
			msg.err = err
			return msg
		}
		defer s.Close()
		deps := run.TestDeps{Cfg: cfg, Workspace: w.Path, Store: s, Session: graph.NewSessionLog(graph.New(s, version, dir)), AskYesNo: confirm, In: strings.NewReader(""), Out: io.Discard}
		selection, err := run.FreezeTests(ctx, deps, ids)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.result, msg.err = run.RunTests(ctx, deps, selection)
		return msg
	}
}
func (m *Model) askTestConfirm(owner string, ownerCtx context.Context) func(context.Context, string) bool {
	shared := m.Shared
	return func(ctx context.Context, prompt string) bool {
		if shared == nil || shared.Prog == nil || ctx.Err() != nil || ownerCtx.Err() != nil {
			return false
		}
		ch := make(chan string, 1)
		msg := showDialogMsg{ctx: ownerCtx, tests: owner, kind: "auth", prompt: prompt, answer: ch}
		shared.Prog.Send(msg)
		defer func() { msg.dismiss = true; shared.Prog.Send(msg) }()
		select {
		case a := <-ch:
			return ctx.Err() == nil && ownerCtx.Err() == nil && (a == "y" || a == "yes")
		case <-ctx.Done():
			return false
		case <-ownerCtx.Done():
			return false
		}
	}
}
func (m *Model) stopTest() {
	if m.Tests.cancel == nil {
		return
	}
	if m.Chat.Dialog != nil && m.Chat.Dialog.Tests == m.Tests.Busy {
		m.declineDialog()
	}
	m.Tests.Stopping = true
	m.Tests.cancel()
	m.alert("info", "stopping test command; waiting for cleanup and result persistence")
}
func (m *Model) onTestDone(msg testsDoneMsg) tea.Cmd {
	if msg.owner != m.Tests.Busy || msg.ctx != m.Tests.ctx {
		return nil
	}
	if m.Chat.Dialog != nil && m.Chat.Dialog.Tests == msg.owner {
		m.declineDialog()
	}
	if m.Tests.cancel != nil {
		m.Tests.cancel()
	}
	m.Tests.Busy, m.Tests.ctx, m.Tests.cancel, m.Tests.Stopping = "", nil, nil, false
	if msg.err != nil {
		m.alert("err", msg.err.Error())
	} else {
		m.alert("info", "test run settled: "+msg.result.Status+"; standalone evidence does not satisfy Chat/PAT checks")
	}
	if m.quitPending {
		return m.requestQuit()
	}
	return m.refreshTests()
}

func (m Model) testsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.Tests.Editing || strings.TrimSpace(m.Input.Value()) != "" {
		return m, nil, false
	}
	switch msg.String() {
	case "up":
		m.Tests.Sel = max(0, m.Tests.Sel-1)
		m.Tests.Scroll = 0
	case "down":
		m.Tests.Sel = min(len(m.testRows())-1, m.Tests.Sel+1)
		m.Tests.Scroll = 0
	case "n":
		m.editTest(true)
	case "e":
		m.editTest(false)
	case " ":
		m.toggleTest()
	case "g":
		next := m.Cfg.Testing
		next.Guidance = !next.Guidance
		m.saveTesting(next)
	case "d":
		m.designTest()
	case "r":
		return m, m.startTest(), true
	case "R":
		return m, m.startTestSelection(true), true
	case "x":
		m.stopTest()
	case "f":
		return m, m.refreshTests(), true
	case "enter":
		m.Tests.Detail = !m.Tests.Detail
		m.Tests.Scroll = 0
		return m, m.refreshTests(), true
	case "pgdown":
		m.Tests.Scroll += max(1, viewHeight(m)-3)
	case "pgup":
		m.Tests.Scroll = max(0, m.Tests.Scroll-max(1, viewHeight(m)-3))
	default:
		return m, nil, false
	}
	return m, nil, true
}
func (m *Model) renderTests(width, height int) string {
	var lines []string
	guidance := "off"
	if m.Cfg.Testing.Guidance {
		guidance = "on"
	}
	lines = append(lines, "Tests · project guidance "+guidance, "n add · e edit · space enable · r selected · R enabled · x cancel · d design · g guidance · Enter results · f refresh · PgUp/PgDn")
	if m.Tests.Busy != "" {
		state := "running"
		if m.Tests.Stopping {
			state = "stopping"
		}
		lines = append(lines, "Test command "+state+"; other workspace operations wait")
	}
	if m.Tests.Editing {
		lines = append(lines, "Editing JSON · stable ID · Enter saves · Escape discards")
	}
	rows := m.testRows()
	startRow := min(max(0, m.Tests.Sel-2), max(0, len(rows)-5))
	endRow := min(len(rows), startRow+5)
	for i := startRow; i < endRow; i++ {
		row := rows[i]
		mark := "  "
		if i == m.Tests.Sel {
			mark = "> "
		}
		state := "enabled"
		if !row.Enabled {
			state = "disabled"
		}
		if row.Required {
			state = "required"
		}
		if row.Command == "" {
			state = "unconfigured"
		}
		lines = append(lines, mark+row.Name+" ["+state+"]")
	}
	if len(rows) > 5 {
		lines = append(lines, fmt.Sprintf("Suites %d–%d/%d · arrows select", startRow+1, endRow, len(rows)))
	}
	s := m.selectedTest()
	lines = append(lines, "", "ID: "+s.ID, "Purpose: "+s.Purpose, "Expected: "+s.Expected, "Command: "+s.Command)
	ready := m.Tests.Readiness[s.ID]
	if s.Command == "" {
		ready = "unconfigured"
	} else if ready == "" {
		ready = "unchecked; f refresh, r validates before execution"
	}
	lines = append(lines, "Runnable: "+ready)
	if s.DisabledReason != "" {
		lines = append(lines, "Disabled: "+s.DisabledReason)
	}
	if s.Location != "" {
		lines = append(lines, "Location: "+s.Location)
	}
	if s.Exclusions != "" {
		lines = append(lines, "Exclusions: "+s.Exclusions)
	}
	for _, p := range s.Prerequisites {
		lines = append(lines, "Prerequisite: "+p.Kind+" "+p.Value)
	}
	if s.SpecKey != "" {
		lines = append(lines, "Spec: "+s.SpecKey+" @ "+s.SpecVersion)
	}
	if s.ReportFormat != "" {
		lines = append(lines, "Report format: "+s.ReportFormat, "Declared required IDs: "+strings.Join(s.ExpectedTests, ", "))
	}
	if s.Required {
		lines = append(lines, "Standalone results do not discharge current Chat/PAT required verification.")
	}
	found := false
	for _, r := range m.Tests.History {
		matchedResult := false
		for _, result := range r.Results {
			if result.SuiteID == s.ID {
				matchedResult = true
				found = true
				fresh := m.Tests.Freshness[r.Selection.ID]
				if fresh == "" {
					fresh = "unknown"
				}
				lines = append(lines, "Result: "+result.Status+" · freshness "+fresh+" at last refresh · coverage "+result.Coverage.Status)
				if m.Tests.Detail {
					lines = append(lines, "Run: "+r.Selection.ID+" · "+r.Ended, "Reason: "+result.Reason, fmt.Sprintf("Coverage expected=%d discovered=%d executed=%d passed=%d failed=%d skipped=%d skipped packages=%d", result.Coverage.Expected, result.Coverage.Discovered, result.Coverage.Executed, result.Coverage.Passed, result.Coverage.Failed, result.Coverage.Skipped, result.Coverage.SkippedPackages), "Coverage reason: "+result.Coverage.Reason)
					if result.Receipt != nil {
						text := result.Receipt.Text
						if chars := []rune(text); len(chars) > 8192 {
							text = string(chars[:8192]) + "\n[display capped; full bounded receipt in archive]"
						}
						lines = append(lines, "Output: "+text)
						if result.Receipt.Truncated {
							lines = append(lines, "Command output was truncated; coverage may be unknown")
						}
					}
					for _, old := range r.Selection.Suites {
						if old.ID == s.ID {
							lines = append(lines, "Recorded command: "+old.Command)
						}
					}
				}
			}
		}
		if !matchedResult {
			for _, selected := range r.Selection.Suites {
				if selected.ID == s.ID {
					found = true
					lines = append(lines, "Result: incomplete · no final command receipt · execution unknown · freshness unknown · coverage unknown", "Run: "+r.Selection.ID)
					break
				}
			}
		}
		if found && !m.Tests.Detail {
			break
		}
	}
	if !found {
		lines = append(lines, "Result: not run · freshness unknown · coverage unknown")
	}
	if m.Tests.Err != "" {
		lines = append(lines, "Evidence read: "+m.Tests.Err)
	}
	wrapped := Wrap(ansi.Strip(strings.Join(lines, "\n")), max(1, width))
	start := min(m.Tests.Scroll, max(0, len(wrapped)-max(1, height)))
	end := min(len(wrapped), start+max(1, height))
	out := append([]string(nil), wrapped[start:end]...)
	for len(out) < height {
		out = append(out, "")
	}
	for i := range out {
		out[i] = ansi.Truncate(out[i], max(1, width), "…")
	}
	return strings.Join(out, "\n")
}
