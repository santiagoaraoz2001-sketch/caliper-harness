package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"os"
	"path/filepath"
	"specific-harness/internal/llm"
	"specific-harness/internal/serve"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/watch"
	"strings"
	"testing"
	"time"
)

// Captures actual Model.View with disclosed fixture state, never a mock frontend.
func TestTUIRefinementGallery(t *testing.T) {
	dir := os.Getenv("TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("opt-in actual renderer gallery")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	for _, sz := range [][2]int{{80, 24}, {120, 40}} {
		m := viewportUpdate(New(testCfg(), "gallery"), tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		m.St.Model, m.ModelName = "Qwen local", "Qwen local"
		m.Cfg.Model.Name = "Qwen local"
		m.St.Branch, m.St.Dir = "main", "specific-harness"
		m.St.CtxWindow = 32768
		capture := func(name string, x Model) {
			t.Helper()
			x.resizeComposer()
			viewportFrame(t, x)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.ansi", name, sz[0])), []byte(x.View()), 0644); err != nil {
				t.Fatal(err)
			}
		}
		capture("empty-chat", m)
		for _, state := range []string{"ready", "issue", "offline"} {
			x := m
			x.WorkerObservation = workerObservation{Server: x.Cfg.Model.Server, Model: x.Cfg.Model.Name, State: state}
			capture("activity-"+state, x)
		}
		busy := m
		busy.Chat.Running = true
		capture("activity-running-frame-0", busy)
		busy.ActivityFrame = 3
		capture("activity-running-frame-3", busy)
		m.Chat.Running, m.Chat.Seq = true, 1
		m.Chat.appendLine(lineUser, "Inspect startup checks, then explain the change.")
		m.onToken(tokenMsg{seq: 1, delta: "I will read the source before making changes."})
		aux, _ := json.Marshal(session.Entry{Message: llm.Message{Role: "assistant", Content: "I will read the source before making changes.", Reasoning: "Compare the current checks with their recorded results. Preserve their existing behavior."}})
		m.onEvent(eventMsg{seq: 1, kind: "native_reply", text: string(aux)})
		m.onEvent(eventMsg{seq: 1, kind: "tool", text: `read {"path":"internal/runtime/checks.go","start_line":24,"end_line":29}`})
		result, _ := json.Marshal(session.Entry{Kind: "observation", Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "read_01", Content: "24 func ready() bool {\n25   if runtime == nil {\n26     return false\n27   }\n28   return runtime.Ready()\n29 }"}})
		m.onEvent(eventMsg{seq: 1, kind: "native_result", text: string(result)})
		m.Chat.Running = false
		capture("readability", m)
		m.Chat.Running = true
		m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 1, Path: "internal/runtime/checks.go", Op: watch.OpModify, BeforeKnown: true, At: time.Date(2026, 10, 13, 1, 30, 0, 0, time.UTC), Removed: []string{"return nil"}, Added: []string{"if runtime == nil {", "  return false", "}", "return runtime.Ready()"}, RemovedN: 1, AddedN: 4, After: []string{"func ready() bool {", "  return runtime.Ready()", "}"}}})
		m.Chat.Running = false
		capture("observed-diff", m)
		m.Chat.Running = true
		m.onEvent(eventMsg{seq: 1, kind: "tool", text: "bash cmd=go test ./internal/runtime\n  && printf '%s\\n' 'check complete'"})
		failure, _ := json.Marshal(session.Entry{Kind: "failure", Message: llm.Message{Role: "tool", Name: "bash", ToolCallID: "check_02", Content: "tool error: dependency missing\nCommand did not run. Install the declared dependency before retrying."}})
		m.onEvent(eventMsg{seq: 1, kind: "native_result", text: string(failure)})
		m.Chat.Running = false
		capture("failed-command", m)
		m.Input.SetValue("/s")
		capture("command-menu", m)
		m.Input.SetValue("")
		m.Chat.pinned = true
		m.Chat.pinTop = 0
		capture("chat-scrollback", m)
		m.Chat.unpin()
		for _, view := range []ViewID{ViewStatus, ViewSettings, ViewFiles, ViewWorkflows, ViewSessions, ViewPlans, ViewDecisions, ViewDiff} {
			x := m
			x.Tabs = append([]Tab(nil), m.Tabs...)
			x.Chat = freshChatState()
			x.navigate(view, "")
			for i := 0; i < 40; i++ {
				x.Files.Files = append(x.Files.Files, FileEntry{Rel: fmt.Sprintf("internal/runtime/source-%02d.go", i)})
				x.Sessions.Rows = append(x.Sessions.Rows, SessionRow{ID: fmt.Sprintf("saved-session-%02d", i), Prompt: "Inspect and preserve the runtime checks", Count: 12, When: time.Date(2026, 10, 12, 20, i, 0, 0, time.UTC)})
				x.Wf.Rows = append(x.Wf.Rows, WfRow{Title: fmt.Sprintf("Runtime verification %02d", i), State: "running", Done: 1, Total: 3, Attempts: 2, Agents: 2, Step: "Inspect runtime checks before executing the declared verification command", Unverified: 1})
				x.Plans.Rows = append(x.Plans.Rows, PlanRow{Kind: rowPlan, Ref: fmt.Sprintf("P%d", i), Label: fmt.Sprintf("Runtime plan %02d", i), State: "draft", Total: 3})
			}
			x.Files.Sel, x.Sessions.Sel, x.Wf.Sel, x.Plans.Sel = 39, 39, 39, 39
			x.Plans.Loaded = true
			x.Serve.Rows = []ServeRow{{Ep: serve.Endpoint{Label: "local", URL: "http://127.0.0.1:8080", Model: "Qwen local"}, IsModel: true, Have: true, Health: serve.Health{Up: true, LatencyMs: 8}}, {Ep: serve.Endpoint{Label: "decision", URL: "http://127.0.0.1:8090"}, Sidecar: true, Have: true, Health: serve.Health{Reachable: true, Err: "not loaded"}}}
			x.Decisions.Rows = []DecisionRow{{Source: "authorization", Pick: "inspect", Conf: .93, Resolution: "Read-only inspection allowed"}}
			x.Decisions.BySource = map[string]int{"authorization": 1}
			capture(string(view), x)
			if view == ViewFiles {
				x.Files.Viewer = &FileView{Title: "internal/runtime/checks.go", Logical: []string{"package runtime", "", "// Preserve the declared readiness check.", "func ready() bool {", "  return runtime.Ready()", "}"}}
				capture("file-viewer", x)
			}
			if view == ViewSettings {
				x.Settings.Sel = len(settingFields()) - 1
				capture("settings-last", x)
			}
		}
		// Opportunity states use the actual renderer and existing typed state.
		x := m
		x.Chat = freshChatState()
		x.navigate(ViewSettings, "")
		for i, f := range settingFields() {
			if f.Section == "params" && f.Key == "top_k" {
				x.Settings.Sel = i
			}
		}
		capture("settings-effect", x)
		x.navigate(ViewWorkflows, "")
		x.Wf.Rows = []WfRow{{Title: "Readiness checks", State: "running", Done: 2, Total: 4, Agents: 2, Attempts: 3, Unverified: 1, Failed: 1, Step: "Review the failed check before retrying the selected task."}}
		capture("workflow-exceptions", x)
		x.navigate(ViewSessions, "")
		x.Sessions.Detail = &SessionDetail{ID: "recorded-example", Events: []session.Event{
			{Kind: "prompt", Text: "Inspect startup checks and preserve their behavior.", Time: time.Date(2026, 10, 13, 1, 30, 0, 0, time.UTC)},
			{Kind: "tool", Text: `read {"path":"internal/runtime/checks.go"}`, Time: time.Date(2026, 10, 13, 1, 30, 2, 0, time.UTC)},
			{Kind: "error", Text: "dependency missing; no check was executed", Time: time.Date(2026, 10, 13, 1, 30, 4, 0, time.UTC)},
		}}
		capture("session-recorded-context", x)
		x.navigate(ViewChat, "")
		x.Chat = freshChatState()
		x.Chat.appendLine(lineTool, `read {"path":"internal/runtime/checks.go"}`)
		x.Chat.ToolCallIDs = map[int]string{0: "read_history"}
		var output []string
		for i := 0; i < 120; i++ {
			output = append(output, fmt.Sprintf("%03d  recorded source row: preserve the actual request and result", i+1))
		}
		x.Chat.appendLine(lineResult, strings.Join(output, "\n"))
		x.Chat.Recorded = map[int]session.Entry{1: {Kind: "observation", Message: llm.Message{Name: "read", ToolCallID: "read_history"}}}
		x.scrollChat(-10)
		x.Input.SetValue("Explain this result")
		capture("chat-wheel-reading", x)
		x.Chat.unpin()
		x.Comment = []Comment{{Source: "files", Ref: "internal/runtime/checks.go", Lines: "[24-29]", Quote: "return runtime.Ready()", Text: "Review this before editing."}}
		capture("queued-context", x)
		x.Comment = nil
		x.Input.SetValue("/comm")
		capture("command-syntax", x)
		x.Input.SetValue("")
		x.Chat.Dialog = &Dialog{Kind: "auth", Prompt: "## Approval request\nInspect the complete command before deciding.\n```sh\ngo test ./internal/runtime \\\n  && printf '%s\\n' 'verification complete'\n```"}
		capture("approval-proof", x)
		x.Chat.Dialog = nil
		lipgloss.SetColorProfile(termenv.Ascii)
		capture("no-color-reading", x)
		lipgloss.SetColorProfile(termenv.TrueColor)
		j := journeyFixture(sz[0], sz[1])
		j.Cfg.Model.Name, j.ModelName, j.St.Model = "Qwen local", "Qwen local", "Qwen local"
		capture("workflow-dock", j)
		j = viewportKey(j, tea.KeyDown)
		capture("workflow-dock-selected", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-plan-summary", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-approach-summary", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-task-summary", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-attempt-history", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-receipt", j)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-raw-record", j)
		j = viewportKey(j, tea.KeyEsc)
		j = viewportKey(j, tea.KeyEsc)
		j = viewportKey(j, tea.KeyDown)
		j = viewportKey(j, tea.KeyEnter)
		capture("workflow-failed-check", j)
		j = viewportKey(j, tea.KeyEsc)
		j = viewportKey(j, tea.KeyEsc)
		capture("workflow-back-to-task", j)
		j.Journey.Tree.Plans[0].Approaches[0].Tasks[0].AttemptLog = "broken"
		capture("workflow-corrupt-evidence", j)
		lipgloss.SetColorProfile(termenv.Ascii)
		capture("workflow-no-color", j)
		lipgloss.SetColorProfile(termenv.TrueColor)
		m.navigate(ViewChat, "")
		edge := viewportUpdate(New(testCfg(), "gallery"), tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		edge.St = m.St
		edge.Chat.Running, edge.Chat.Seq = true, 1
		first := session.Entry{Message: llm.Message{Role: "assistant", Reasoning: "First recorded reasoning pass."}}
		b, _ := json.Marshal(first)
		edge.onEvent(eventMsg{seq: 1, kind: "native_reply", text: string(b)})
		first.Message.Reasoning = "Second recorded reasoning pass, after checking the receipt."
		b, _ = json.Marshal(first)
		edge.onEvent(eventMsg{seq: 1, kind: "native_reply", text: string(b)})
		calls := []llm.ToolCall{{ID: "edit_refused", Name: "edit", Arguments: `{"path":"internal/runtime/checks.go","before":"return nil","after":"return false"}`}, {ID: "check_refused", Name: "bash", Arguments: `{"cmd":"go test ./internal/runtime && printf '%s\\n' 'verification complete'"}`}}
		b, _ = json.Marshal(session.Entry{Message: llm.Message{Role: "assistant", ToolCalls: calls}})
		edge.onEvent(eventMsg{seq: 1, kind: "native_reply", text: string(b)})
		for _, call := range calls {
			b, _ = json.Marshal(session.Entry{Kind: "failure", Message: llm.Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: "refused: entire array; no calls ran"}})
			edge.onEvent(eventMsg{seq: 1, kind: "native_result", text: string(b)})
		}
		edge.Chat.Running = false
		capture("refused-native-array", edge)
		edge.Chat = freshChatState()
		edge.Chat.Running, edge.Chat.Seq = true, 1
		edge.onEvent(eventMsg{seq: 1, kind: "tool", text: "bash cmd=printf '" + strings.Repeat("long argument ", 18) + "REQUEST_TAIL'\n  && printf '%s\\n' 'literal key=value'"})
		edge.onEvent(eventMsg{seq: 1, kind: "result", text: strings.Repeat("Recorded result row\n", 9) + "RESULT_TAIL"})
		edge.Chat.Running = false
		edge.Chat.pinned = true
		capture("long-command-scrollback", edge)
		edge.Chat = freshChatState()
		edge.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 99, Path: "partial.go", BeforeKnown: true, Added: []string{"return true"}, AddedN: 20}})
		capture("capped-diff", edge)
		edge.navigate(ViewSettings, "")
		edge.Cfg.Decision.Path = "/LONG_PATH/" + strings.Repeat("界folder/", 35) + "VALUE_TAIL"
		edge.Settings.DecisionNotice = "Requested decision selection is staged. Native activation has not been confirmed."
		for i, f := range settingFields() {
			if f.Section == "decision" && f.Key == "path" {
				edge.Settings.Sel = i
				break
			}
		}
		capture("settings-long-value", edge)
		edge = key(edge, "enter")
		capture("settings-long-value-edit", edge)

		history := historyFixture(sz[0], sz[1])
		history.St = m.St
		history = submit(history, "/open-diff 15")
		capture("captured-file", history)
		history = submit(history, "/changes")
		capture("file-history", history)
		history = viewportKey(history, tea.KeyPgUp)
		capture("file-history-older-page", history)
		history = submit(history, "/changes never-seen.go")
		capture("file-history-empty", history)
		history = submit(history, "/open-diff 15")
		history.Files.Capture.Truncated = true
		capture("captured-file-partial", history)
		edge = viewportUpdate(New(testCfg(), "gallery"), tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		edge.St = m.St
		ev := watch.Event{Seq: 42, Path: "large.go", Op: watch.OpModify, BeforeKnown: true, After: []string{"package example", "// Snapshot remains available beyond preview limits."}}
		for i := 0; i < 20; i++ {
			ev.Removed = append(ev.Removed, fmt.Sprintf("return old_%d", i))
			ev.Added = append(ev.Added, fmt.Sprintf("return new_%d", i))
		}
		ev.AddedN, ev.RemovedN = 20, 20
		edge.onDiffEvent(diffEventMsg{ev: ev})
		capture("bounded-chat-diff", edge)

		m.Chat.Dialog = &Dialog{Kind: "auth", Prompt: "bash · go test ./internal/runtime\nRun the declared check in this workspace?\nCommand: go test ./internal/runtime", Answer: make(chan string, 1)}
		capture("approval", m)
		m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "Which area should we inspect next?", Options: []string{"Runtime checks", "Conversation and file changes", "Workflow state"}}}, AskDone: make(chan []ask.Answer, 1)}
		capture("question", m)
		m.Chat.Running = true
		m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "What should the model inspect next?"}}, AskDone: make(chan []ask.Answer, 1)}
		capture("free-text-question", m)
		m.Chat.Dialog = nil
		m.Chat.Streaming, m.Chat.Running = true, true
		m.Chat.Stream.Reset()
		capture("waiting", m)
		m.Chat.Running = false
		m.Input.SetValue(strings.Repeat("multiline draft\n", 7))
		capture("multiline-composer", m)
	}
}
