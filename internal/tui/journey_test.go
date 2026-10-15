package tui

import (
	"encoding/json"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"strings"
	"testing"
	"time"
)

func journeyFixture(width, height int) Model {
	m := viewportUpdate(New(testCfg(), "journey"), tea.WindowSizeMsg{Width: width, Height: height})
	m.St.Branch, m.St.Dir = "main", "specific-harness"
	entry, _ := json.Marshal(session.Entry{Kind: "observation", Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "read-startup", Content: "package runtime\nreturn runtime.Ready()"}})
	events := []session.Event{{Kind: "tool", Text: `read {"path":"internal/runtime/checks.go"}`}, {Kind: "native_result", Text: string(entry)}}
	log, _ := json.Marshal([]any{
		map[string]any{"Kind": "execution", "Attempt": 1, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Unverified, "ExecutionEvents": events},
		map[string]any{"Kind": "check", "Attempt": 1, "Outcome": outcome.Outcome{Status: outcome.Failed, Reason: "dependency missing"}, "Verification": outcome.Rejected, "Command": "go test ./internal/runtime", "CheckEvents": []session.Event{{Kind: "error", Text: "dependency missing; check did not pass"}}},
		map[string]any{"Kind": "check", "Attempt": 1, "Outcome": outcome.Outcome{Status: outcome.Completed}, "Verification": outcome.Unverified, "Command": "go test ./internal/runtime"},
		map[string]any{"Kind": "comment", "Comment": outcome.TaskComment{TaskID: "task-one", Attempt: 1, Text: "Review this receipt."}},
	})
	t := &plan.Task{ID: "task-one", Title: "Inspect startup checks", State: plan.TaskDone, Execution: outcome.Completed, Verification: outcome.Unverified, Attempts: 1, AttemptLog: string(log), Current: "Preserve the current dependency checks.", Target: "A readable startup screen.", Acceptance: "No runtime behavior changes."}
	lp := LoadedPlan{P: &plan.Plan{ID: "plan-one", Title: "Startup reliability", Goal: "Make startup checks understandable and easy to recover.", State: plan.PlanActive, CreatedAt: time.Now()}, Approaches: []LoadedApproach{
		{A: &plan.Approach{ID: "approach-one", Title: "Dependency checks", State: plan.ApproachRunning, CreatedAt: time.Now()}, Tasks: []*plan.Task{t, {ID: "task-two", Title: "Review failure recovery", State: plan.TaskQueued, Execution: outcome.Unknown, Verification: outcome.Unverified}}},
		{A: &plan.Approach{ID: "approach-two", Title: "Recovery flow", State: plan.ApproachPaused, CreatedAt: time.Now().Add(time.Second)}, Tasks: []*plan.Task{{ID: "task-three", Title: "Inspect retry guidance", State: plan.TaskQueued}}},
	}}
	tree := PlansState{Plans: []LoadedPlan{lp}}
	tree.rebuild("")
	m.Journey = journeyState{Tree: tree, Loaded: true}
	m.Chat.appendLine(lineUser, "Inspect startup checks, then explain the change.")
	m.Chat.appendLine(lineAssistant, "The observed check still needs verification before qualification.")
	return m
}
func TestJourneyRoundTripAndOriginalHistory(t *testing.T) {
	m := journeyFixture(80, 24)
	before := m.Chat.Context.Clone()
	m = viewportKey(m, tea.KeyDown)
	if !m.Tabs[m.Active].dock.Focused || m.Input.Focused() {
		t.Fatal("empty composer does not select dock")
	}
	m = viewportKey(m, tea.KeyEnter)
	if !m.journeyActive() || m.Tabs[m.Active].at.journey.Kind != rowPlan {
		t.Fatal("enter did not inspect plan")
	}
	m = viewportKey(m, tea.KeyDown)
	planChoice := m.Tabs[m.Active].at.journey.Selected
	m = viewportKey(m, tea.KeyEnter)
	if m.Tabs[m.Active].at.target != "approach-two" {
		t.Fatal("selected approach wrong")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Tabs[m.Active].at.journey.Kind != rowPlan || m.Tabs[m.Active].at.journey.Selected != planChoice {
		t.Fatal("Escape lost one-layer selection")
	}
	m = key(m, "ctrl+g")
	if m.Tabs[m.Active].at.target != "approach-two" {
		t.Fatal("original forward history lost")
	}
	m = key(m, "ctrl+o")
	if m.Tabs[m.Active].at.journey.Selected != planChoice {
		t.Fatal("original back history lost")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Tabs[m.Active].at.view != ViewChat || !m.Tabs[m.Active].dock.Focused {
		t.Fatal("root return did not restore dock")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Tabs[m.Active].dock.Focused || !m.Input.Focused() {
		t.Fatal("dock Escape did not restore input")
	}
	if !reflect.DeepEqual(before, m.Chat.Context) || m.Chat.Running || m.Wf.Busy != "" {
		t.Fatal("inspection changed execution authority")
	}
}
func TestJourneyAttemptsReceiptsRawAndBack(t *testing.T) {
	m := journeyFixture(80, 24)
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	if m.Tabs[m.Active].at.journey.Kind != "attempt" {
		t.Fatal("task did not open recorded attempt")
	}
	v := viewportFrame(t, m)
	for _, s := range []string{"Record #0", "Record #1", "Record #2", "Record #3", "comment"} {
		if !strings.Contains(v, s) {
			t.Fatalf("record missing:%s", s)
		}
	}
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyDown)
	scroll := m.Tabs[m.Active].at.journey.Scroll
	m = viewportKey(m, tea.KeyEnter)
	if m.Tabs[m.Active].at.journey.Kind != "rawrecord" {
		t.Fatal("raw source unreachable")
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Tabs[m.Active].at.journey.Kind != "receipt" || m.Tabs[m.Active].at.journey.Scroll != scroll {
		t.Fatal("raw return lost receipt scroll")
	}
	for _, kind := range []string{"attempt", rowTask, rowApproach} {
		m = viewportKey(m, tea.KeyEsc)
		if m.Tabs[m.Active].at.journey.Kind != kind {
			t.Fatalf("escape skipped layer:%s", kind)
		}
	}
	m = viewportKey(m, tea.KeyEsc)
	if m.Tabs[m.Active].at.view != ViewChat || m.Tabs[m.Active].dock.Selected != "approach-one" {
		t.Fatal("shortcut did not return to its entry")
	}
}
func TestJourneyPreservesDraftDialogAndRunOwnership(t *testing.T) {
	for _, text := range []string{" ", "draft text", "first\nsecond"} {
		m := journeyFixture(80, 24)
		m.Input.SetValue(text)
		m = viewportKey(m, tea.KeyDown)
		if m.Tabs[m.Active].dock.Focused || m.Input.Value() != text {
			t.Fatal("dock stole draft arrow")
		}
	}
	m := journeyFixture(80, 24)
	m = viewportKey(m, tea.KeyDown)
	m = key(m, "l")
	if m.Input.Value() != "l" || m.Tabs[m.Active].dock.Focused || m.Wf.Busy != "" {
		t.Fatal("typing lost rune or launched workflow")
	}
	m = journeyFixture(80, 24)
	m.Chat.Running = true
	cancelled := false
	m.Chat.cancel = func() { cancelled = true }
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEsc)
	if cancelled || !m.Chat.Running {
		t.Fatal("journey Escape cancelled active chat")
	}
	m = viewportKey(m, tea.KeyEsc)
	if cancelled {
		t.Fatal("dock Escape stopped run")
	}
	m = viewportKey(m, tea.KeyEsc)
	if !cancelled {
		t.Fatal("original chat Escape no longer stops")
	}
	m = journeyFixture(80, 24)
	m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "Pick one", Options: []string{"one", "two"}}}, AskDone: make(chan []ask.Answer, 1)}
	m = viewportKey(m, tea.KeyDown)
	if m.Chat.Dialog.PickIdx != 1 || m.Tabs[m.Active].dock.Focused {
		t.Fatal("dock stole question")
	}
}
func TestJourneyStaleRefreshRemovalAndReceiptBinding(t *testing.T) {
	m := journeyFixture(80, 24)
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyEnter)
	old := journeyLoadedMsg{plans: m.Journey.Tree.Plans, request: m.Journey.Request, owner: m.Chat.ID, path: m.StorePath, route: m.journeyRoute()}
	m = viewportKey(m, tea.KeyEnter)
	before := m.Tabs[m.Active].at
	m.onJourneyLoaded(old)
	if m.Tabs[m.Active].at != before {
		t.Fatal("stale refresh replaced route")
	}
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyEnter)
	task := m.Journey.Tree.Plans[0].Approaches[0].Tasks[0]
	task.AttemptLog = `[{"Kind":"execution","Attempt":1,"Output":"replacement"}]`
	if !strings.Contains(viewportFrame(t, m), "changed or is unavailable") {
		t.Fatal("receipt replacement displayed as original")
	}
	task.AttemptLog = `broken`
	if !strings.Contains(viewportFrame(t, m), "unreadable") {
		t.Fatal("corrupt receipt treated as empty success")
	}
}
func TestJourneyDensityAndTabs(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 12}, {34, 20}} {
		m := journeyFixture(size[0], size[1])
		m.Input.SetValue("first\nsecond\nthird")
		m.resizeComposer()
		viewportFrame(t, m)
		if len(m.dockRows()) > 3 {
			t.Fatal("dock too tall")
		}
		m.Input.SetValue("")
		m.resizeComposer()
		if len(m.dockRows()) > 0 {
			m = viewportKey(m, tea.KeyDown)
			m = viewportKey(m, tea.KeyEnter)
		} else {
			m.openJourney(journeyChoice{target: "plan-one", frame: journeyFrame{Kind: rowPlan}})
		}
		viewportFrame(t, m)
	}
	m := journeyFixture(80, 24)
	m = viewportKey(m, tea.KeyDown)
	m = viewportKey(m, tea.KeyEnter)
	m = viewportKey(m, tea.KeyDown)
	want := m.Tabs[m.Active].at
	m = key(m, "ctrl+t")
	m = key(m, "ctrl+p")
	if m.Tabs[m.Active].at != want {
		t.Fatal("tab switch lost journey frame")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Recovery flow") {
		t.Fatal("restored selected approach absent")
	}
}

func TestJourneyFocusTransitions(t *testing.T) {
	for _, transition := range []string{"resize", "draft", "history", "free answer"} {
		t.Run(transition, func(t *testing.T) {
			m := journeyFixture(80, 24)
			m.Drafts[1] = "retained draft"
			m.Hist = []string{"retained history"}
			m = viewportKey(m, tea.KeyDown)
			switch transition {
			case "resize":
				m = viewportUpdate(m, tea.WindowSizeMsg{Width: 40, Height: 12})
			case "draft":
				m = viewportKey(m, tea.KeyShiftRight)
			case "history":
				m = viewportKey(m, tea.KeyShiftUp)
			case "free answer":
				m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "Explain the approach"}}, AskDone: make(chan []ask.Answer, 1)}
				m.syncAskInput(true)
				m = viewportUpdate(m, nil)
			}
			if !m.Input.Focused() || m.Tabs[m.Active].dock.Focused {
				t.Fatal("composer focus was not restored")
			}
			m = key(m, "z")
			if !strings.Contains(m.Input.Value(), "z") {
				t.Fatal("typing was lost")
			}
		})
	}
}

func TestJourneyEndThenUpAndFullBrief(t *testing.T) {
	m := journeyFixture(80, 24)
	m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: "brief"}})
	m = viewportKey(m, tea.KeyEnd)
	bottom := m.Tabs[m.Active].at.journey.Scroll
	maxScroll := max(0, len(m.journeyDetailRows(m.Width))-m.journeyBodyHeight(m.Width, viewHeight(m)))
	if bottom != maxScroll {
		t.Fatalf("stored End scroll=%d, max=%d", bottom, maxScroll)
	}
	m = viewportKey(m, tea.KeyUp)
	if m.Tabs[m.Active].at.journey.Scroll != max(0, bottom-1) {
		t.Fatal("End then Up did not move one row")
	}
	task := m.Journey.Tree.Plans[0].Approaches[0].Tasks[0]
	task.Prompt, task.CheckCommand, task.AcceptanceCommand = "original task prompt", "original project check", "original acceptance check"
	rows := strings.Join(m.journeyDetailRows(80), "\n")
	for _, s := range []string{task.Prompt, task.CheckCommand, task.AcceptanceCommand} {
		if !strings.Contains(rows, s) {
			t.Fatalf("full brief omitted %s", s)
		}
	}
}

func TestJourneyCompactDetailTailReachable(t *testing.T) {
	for _, h := range []int{2, 3} {
		m := journeyFixture(40, 12)
		task := m.Journey.Tree.Plans[0].Approaches[0].Tasks[0]
		task.AttemptLog = ""
		task.Result = strings.Repeat("saved evidence\n", 8) + "LAST_RETAINED_ROW"
		m.openJourney(journeyChoice{target: "task-one", frame: journeyFrame{Kind: "brief"}})
		m.Tabs[m.Active].at.journey.Scroll = max(0, len(m.journeyDetailRows(40))-m.journeyBodyHeight(40, h))
		if !strings.Contains(ansi.Strip(m.renderJourney(40, h)), "LAST_RETAINED_ROW") {
			t.Fatalf("tail unreachable at body height %d", h)
		}
	}
}
