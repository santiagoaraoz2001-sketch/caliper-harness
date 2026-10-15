package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func displayStep(m Model, msg tea.Msg) Model { next, _ := m.Update(msg); return next.(Model) }
func displayText(m *Model) string {
	var b strings.Builder
	for _, row := range chatRows(m, 120) {
		b.WriteString(row.text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestReplyDisplayMalformedFragmentsAndRaw(t *testing.T) {
	raw := "before\r\n```tool\r\ntool: bash\r\ncmd: |\r\n  cat <<END\r\n  ```\r\n  HIDDEN_PAYLOAD  \r\n  ```\r\n  END\r\n```\r\nafter"
	for _, chunk := range []int{1, 7} {
		m := New(testCfg(), "display")
		m.Cfg.Tabs.TitleEvery = 0
		m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 1
		dir := t.TempDir()
		log := fanoutLog{logs: []agent.SessionLog{displayRawSession{dir}, progLog{seq: 1, send: func(msg tea.Msg) { m = displayStep(m, msg) }}}}
		for n := 0; n < 5; n++ {
			for i := 0; i < len(raw); i += chunk {
				m = displayStep(m, tokenMsg{seq: 1, delta: raw[i:min(i+chunk, len(raw))]})
				if strings.Contains(displayText(&m), "HIDDEN_PAYLOAD") {
					t.Fatal("live malformed payload exposed")
				}
			}
			_, found, repair := tool.Extract(raw)
			if !found || repair == "" {
				t.Fatal("historical malformed body accepted")
			}
			if err := log.Append(context.Background(), "raw", "reply", raw); err != nil {
				t.Fatal(err)
			}
			if err := log.Append(context.Background(), "raw", "result", repair); err != nil {
				t.Fatal(err)
			}
			if m.Chat.Stream.Len() != 0 || strings.Contains(displayText(&m), "HIDDEN_PAYLOAD") || !strings.Contains(displayText(&m), "before") {
				t.Fatal("completed malformed reply display")
			}
		}
		m = displayStep(m, runDoneMsg{seq: 1, final: "stopped: refused calls", in: 23, out: 45})
		events, err := session.Read(dir, "raw")
		if err != nil || len(events) != 10 || m.Chat.FlushedAll.String() != strings.Repeat(raw, 5) || m.Chat.LastIn != 23 || m.Chat.LastOut != 45 {
			t.Fatal("raw archive/usage changed", err)
		}
		for i, e := range events {
			if i%2 == 0 && e.Text != raw {
				t.Fatal("raw session reply changed")
			}
		}
	}
}

func TestReplyDisplayNativeBoundaryAndControls(t *testing.T) {
	for _, kind := range []string{"reply", "native_reply"} {
		m := New(testCfg(), "display")
		m.Cfg.Tabs.TitleEvery = 0
		m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 4
		m = displayStep(m, tokenMsg{seq: 4, delta: "completed prose"})
		payload := "completed prose"
		if kind == "native_reply" {
			b, _ := json.Marshal(session.Entry{Message: llm.Message{Role: "assistant", Content: payload, Reasoning: "original thought"}, Kind: "reply"})
			payload = string(b)
		}
		m = displayStep(m, eventMsg{seq: 3, kind: kind, text: payload})
		if m.Chat.Stream.String() != "completed prose" {
			t.Fatal("stale event flushed current stream")
		}
		m = displayStep(m, eventMsg{seq: 4, kind: kind, text: payload})
		m = displayStep(m, eventMsg{seq: 4, kind: kind, text: payload})
		if m.Chat.Stream.Len() != 0 || len(m.Chat.Lines) != 1 || m.Chat.Lines[0] != (Line{lineAssistant, "completed prose"}) || m.Chat.FlushedAll.String() != "completed prose" {
			t.Fatal("reply missing or duplicated", kind)
		}
		result, _ := json.Marshal(session.Entry{Message: llm.Message{Role: "tool", Content: "refused; no effects", ToolCallID: "actual-id"}})
		m = displayStep(m, eventMsg{seq: 4, kind: "native_result", text: string(result)})
		m = displayStep(m, runDoneMsg{seq: 4, final: "completed prose", in: 12, out: 5})
		if len(m.Chat.Lines) != 2 || m.Chat.Lines[1] != (Line{lineResult, "refused; no effects"}) {
			t.Fatal("native timeline or final duplicate", m.Chat.Lines)
		}
		m = displayStep(m, eventMsg{seq: 4, kind: kind, text: payload})
		if len(m.Chat.Lines) != 2 {
			t.Fatal("settled event appended")
		}
	}
	m := New(testCfg(), "display")
	m.Cfg.Tabs.TitleEvery = 0
	m.Chat.Running, m.Chat.Seq = true, 2
	m = displayStep(m, eventMsg{seq: 2, kind: "native_reply", text: `{"not":"visible"}`})
	m = displayStep(m, runDoneMsg{seq: 2, final: "streamless prose"})
	if len(m.Chat.Lines) != 1 || m.Chat.Lines[0] != (Line{lineAssistant, "streamless prose"}) {
		t.Fatal("streamless final lost/JSON leaked")
	}
}

func TestReplyDisplayOrdinaryFencesAndContinuation(t *testing.T) {
	for _, fence := range []string{"```python", "````markdown", "~~~text"} {
		close := strings.Fields(fence)[0]
		for len(close) > 0 && close[len(close)-1] != '`' && close[len(close)-1] != '~' {
			close = close[:len(close)-1]
		}
		raw := "before\n" + fence + "\n```tool\ntool: bash\ncmd: visible data\n```\n" + close + "\nafter"
		if _, found, _ := tool.Extract(raw); found {
			t.Fatal("ordinary code unexpectedly executable")
		}
		if stripToolBlocks(raw) != raw {
			t.Fatal("ordinary nested source hidden", fence)
		}
	}
	raw := "before\n```tool\ntool: bash\ncmd: echo scalar\nunmarked continuation\n```\nHIDDEN_PAYLOAD"
	if stripToolBlocks(raw) != "before" {
		t.Fatal("unmarked continuation tail exposed")
	}
	for _, raw := range []string{"before\n```tool\nnot valid\n```\nafter", "before\n```tool extra\nSECRET_PAYLOAD\n```\nafter"} {
		if stripToolBlocks(raw) != "before\nafter" {
			t.Fatal("reliable malformed closure lost surrounding prose")
		}
	}
}

func TestReplyDisplayUnsupportedDeclarations(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, body := range []string{"tool: |", "tool: > # comment", "tool: bash\ncmd: | # comment"} {
			t.Run(body+newline, func(t *testing.T) {
				raw := "before" + newline + "```tool" + newline + strings.ReplaceAll(body, "\n", newline) + newline + "  ```" + newline + "  DECLARATION_BODY_SECRET" + newline + "  ```" + newline + "```" + newline + "tail"
				call, found, repair := tool.Extract(raw)
				wantName := strings.TrimPrefix(body, "tool: ")
				if strings.Contains(body, "cmd:") {
					wantName = "bash"
					if len(call.Slots) != 1 || call.Slots["cmd"] != "| # comment" {
						t.Fatal("inline # scalar bytes changed", call)
					}
				} else if len(call.Slots) != 0 {
					t.Fatal("invented declaration slots", call)
				}
				if !found || repair != "" || call.Name != wantName {
					t.Fatal("executable parse changed", call, found, repair)
				}
				reg := tool.NewRegistry()
				reg.Register(tool.Definition{Name: "bash"})
				if body != "tool: bash\ncmd: | # comment" {
					if _, known := reg.Lookup(call.Name); known {
						t.Fatal("malformed declaration became registered Bash")
					}
				}
				m := New(testCfg(), "display")
				m.Cfg.Tabs.TitleEvery = 0
				m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 8
				for i := 0; i < len(raw); i++ {
					m = displayStep(m, tokenMsg{seq: 8, delta: raw[i : i+1]})
					if strings.Contains(m.View(), "DECLARATION_BODY_SECRET") {
						t.Fatal("actual live View exposes ambiguous payload")
					}
				}
				m = displayStep(m, eventMsg{seq: 8, kind: "reply", text: raw})
				m = displayStep(m, eventMsg{seq: 8, kind: "result", text: "refused; no calls ran"})
				m = displayStep(m, runDoneMsg{seq: 8, final: "unverified; no calls ran"})
				if strings.Contains(m.View(), "DECLARATION_BODY_SECRET") || m.Chat.FlushedAll.String() != raw || !strings.Contains(displayText(&m), "refused; no calls ran") {
					t.Fatal("settled display/raw/refusal changed")
				}
			})
		}
	}
	for _, value := range []string{`'| # comment'`, `"| # comment"`, "echo x | cat # comment", "| ordinary bytes"} {
		raw := "before\n```tool\ntool: bash\ncmd: " + value + "\n```\nafter"
		call, found, repair := tool.Extract(raw)
		if !found || repair != "" || call.Name != "bash" || call.Slots["cmd"] != value || stripToolBlocks(raw) != "before\nafter" {
			t.Fatal("ordinary scalar/quoted bytes or prose changed", value)
		}
	}
}
