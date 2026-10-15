package tui

import (
	"encoding/json"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/watch"
	"strings"
	"testing"
)

func TestRefinementFreeTextQuestionGuidance(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: width, Height: 24})
		m.Chat.Running = true
		m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "What next?"}}, AskDone: make(chan []ask.Answer, 1)}
		m.Input.SetValue("preserve the typed answer")
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "Enter answer") || strings.Contains(view, "Enter /command") {
			t.Fatal("free-text question mislabels Enter")
		}
		if m.Input.Value() != "preserve the typed answer" {
			t.Fatal("render mutated the answer")
		}
	}
}

// Existing public UI operations, with a frozen loss-of-content assertion.
func TestRefinementRetainsCompleteCommandsAndOutputs(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running, m.Chat.Seq = true, 1
	raw := "bash cmd=printf '" + strings.Repeat("long argument ", 25) + "REQUEST_TAIL'\n  && printf 'literal key=value'"
	m.onEvent(eventMsg{seq: 1, kind: "tool", text: raw})
	output := "first\nsecond\nthird\nfourth\nRESULT_TAIL"
	m.onEvent(eventMsg{seq: 1, kind: "result", text: output})
	if m.Chat.Lines[0].Text != raw || m.Chat.Lines[1].Text != output {
		t.Fatal("recorded request or result was truncated")
	}
	view := ansi.Strip(m.renderChat(80, 80))
	if !strings.Contains(view, "REQUEST_TAIL") || !strings.Contains(view, "RESULT_TAIL") || !strings.Contains(view, "literal key=value") {
		t.Fatal("complete request or output unavailable in scrollback")
	}
}
func TestRefinementNativeRequestReceiptsAndThinkingAreDisplayOnly(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running, m.Chat.Seq = true, 1
	m.Input.SetValue("keep draft")
	before := m.Chat.Context.Clone()
	call := llm.ToolCall{ID: "refused_01", Name: "bash", Arguments: `{"cmd":"printf 'DO_NOT_EXECUTE'"}`}
	entry := session.Entry{Message: llm.Message{Role: "assistant", Reasoning: "First recorded reasoning", ToolCalls: []llm.ToolCall{call}}}
	send := func(kind string, e session.Entry) {
		raw, _ := json.Marshal(e)
		m.onEvent(eventMsg{seq: 1, kind: kind, text: string(raw)})
	}
	send("native_reply", entry)
	send("native_reply", entry)
	entry.Message.Reasoning = "Second recorded reasoning"
	send("native_reply", entry)
	send("native_result", session.Entry{Kind: "failure", Message: llm.Message{Role: "tool", Name: "bash", ToolCallID: call.ID, Content: "refused: entire array; no calls ran"}})
	m.onEvent(eventMsg{seq: 1, kind: "reasoning", text: "ARCHIVE_JSON_FRAGMENT_NOT_THOUGHT"})
	view := ansi.Strip(m.renderChat(100, 40))
	for _, want := range []string{"First recorded reasoning", "Second recorded reasoning", "DO_NOT_EXECUTE", "refused_01", "no calls ran"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing original event: %s", want)
		}
	}
	if strings.Count(view, "First recorded reasoning") != 1 || strings.Contains(view, "ARCHIVE_JSON_FRAGMENT_NOT_THOUGHT") {
		t.Fatal("duplicate thought or archive fragment leakage")
	}
	if !reflect.DeepEqual(before, m.Chat.Context) || m.Input.Value() != "keep draft" || len(m.Chat.Lines) != 1 {
		t.Fatal("display projection changed context, draft, or raw transcript")
	}
	// A real invocation is displayed once, with the recorded call ID.
	n := New(testCfg(), "test")
	n.Chat.Running, n.Chat.Seq = true, 1
	raw, _ := json.Marshal(session.Entry{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}})
	n.onEvent(eventMsg{seq: 1, kind: "native_reply", text: string(raw)})
	n.onEvent(eventMsg{seq: 1, kind: "tool", text: call.Name + " " + call.Arguments})
	if got := ansi.Strip(n.renderChat(100, 40)); strings.Count(got, "DO_NOT_EXECUTE") != 1 || !strings.Contains(got, call.ID) {
		t.Fatal("structured and invocation views duplicated or lost ID")
	}
}
func TestRefinementOriginalControlsAndDenseLists(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		m := viewportUpdate(New(testCfg(), "test"), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := viewportFrame(t, m)
		for _, want := range []string{"ctrl+t new tab", "ctrl+n/p switch tab", "ctrl+w close tab", "ctrl+o/g tab back/fwd", "→ marks tabs", "⇧←→ drafts", "⇧↑↓ history"} {
			if !strings.Contains(v, want) {
				t.Fatalf("control missing at %d: %s", size[0], want)
			}
		}
		// Literal Tab display is still a legend; Right still moves the caret.
		m.Input.SetValue("left\tright")
		m.Input.SetCursor(0)
		m = viewportKey(m, tea.KeyRight)
		if m.Input.Value() != "left\tright" || m.Input.LineInfo().CharOffset != 1 {
			t.Fatal("legend changed caret or literal tab behavior")
		}
		for i := 0; i < 40; i++ {
			m.Files.Files = append(m.Files.Files, FileEntry{Rel: "fixture" + strings.Repeat("x", i)})
		}
		m.Files.Sel = 39
		m.navigate(ViewFiles, "")
		if !strings.Contains(viewportFrame(t, m), "40/40") || !strings.Contains(m.renderFiles(size[0], viewHeight(m)), "fixture"+strings.Repeat("x", 39)) {
			t.Fatal("final selected file is unreachable")
		}
	}
}
func TestRefinementCappedDiffDisclosesMissingSource(t *testing.T) {
	m := New(testCfg(), "test")
	m.onDiffEvent(diffEventMsg{ev: watch.Event{Seq: 1, Path: "source.go", BeforeKnown: true, Added: []string{"return true"}, AddedN: 20}})
	if v := ansi.Strip(m.renderChat(100, 30)); !strings.Contains(v, "19 changed source lines not retained") || !strings.Contains(v, "/open-diff 1") {
		t.Fatal("diff implies full capture or direct navigation")
	}
}
