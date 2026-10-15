package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

const displayLiteralFixture = "before\n```tool\ntool: bash\ncmd: <<END\ncat <<'DATA'\n```tool\ntool: literal data\n```\nPAYLOAD_AFTER_INNER_FENCE\nDATA\nEND\n```\nafter"

func TestToolDisplayIndependentLiteralFence(t *testing.T) {
	if got := stripToolBlocks(displayLiteralFixture); got != "before\nafter" {
		t.Fatalf("exact independent renderer fixture leaked payload: %q", got)
	}
}
func TestToolDisplayBoundaries(t *testing.T) {
	block := "```tool\ntool: bash\ncmd: true\n```"
	literal := "```tool\ntool: bash\ncmd: <<END\n# comment\n\n```\nSECRET_PAYLOAD\nEND \n END\nEND\n```"
	for _, tc := range []struct{ name, input, want string }{
		{"prose", "before\n\nafter", "before\n\nafter"},
		{"ordinary code", "before\n```python\ncmd = 'visible'\n```\nafter", "before\n```python\ncmd = 'visible'\n```\nafter"},
		{"toolbox code", "```toolbox\nvisible\n```", "```toolbox\nvisible\n```"},
		{"scalar", "before\n" + block + "\nafter", "before\nafter"},
		{"literal comments whitespace and fences", "before\n" + literal + "\nafter", "before\nafter"},
		{"CRLF", strings.ReplaceAll("before\n"+literal+"\nafter", "\n", "\r\n"), "before\r\nafter"},
		{"outer comment cannot arm", "before\n```tool\n# cmd: <<END\ntool: bash\ncmd: true\n```\nafter", "before\nafter"},
		{"multiple", "before\n" + block + "\nbetween\n" + literal + "\nafter", "before\nbetween\nafter"},
		{"malformed body", "before\n```tool\nnot valid\n```\nafter", "before\nafter"},
		{"malformed header", "before\n```tool extra\nSECRET_PAYLOAD\n```\nafter", "before\nafter"},
		{"invalid literal hides remaining ambiguous tail", "before\n```tool\ntool: bash\ncmd: <<BAD LABEL\n```\nSECRET_PAYLOAD\nBAD LABEL\n```\nafter", "before"},
		{"unclosed block", "before\n```tool\ntool: bash\ncmd: true", "before"},
		{"unclosed literal", "before\n```tool\ntool: bash\ncmd: <<END\n```\nSECRET_PAYLOAD", "before"},
		{"unterminated literal declaration", "before\n```tool\ntool: bash\ncmd: <<\n```\nSECRET_PAYLOAD", "before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripToolBlocks(tc.input); got != tc.want {
				t.Fatalf("visible=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestToolDisplayUnfinishedLiteralPrefixes(t *testing.T) {
	// Start after the opening tool line is complete. Header fragments carry
	// no payload; every subsequent prefix must hide the literal, not alter it.
	end := strings.LastIndex(displayLiteralFixture, "\n```")
	for n := strings.Index(displayLiteralFixture, "\ntool:"); n <= end; n++ {
		prefix := displayLiteralFixture[:n]
		if got := stripToolBlocks(prefix); got != "before" {
			t.Fatalf("prefix %d leaked payload: %q", n, got)
		}
	}
}

type displayRawSession struct{ dir string }

func (s displayRawSession) Append(_ context.Context, id, kind, text string) error {
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

func TestToolDisplayLiveFlushAndRawSession(t *testing.T) {
	cmd := "# retained comment\r\ncat <<'DATA'\r\n```tool\r\ntool: literal data\r\n```\r\nSECRET_PAYLOAD  \r\nDATA\r\n"
	raw := "before\r\n```tool\r\ntool: bash\r\ncmd: <<END\r\n" + cmd + "END\r\n```\r\nafter"
	call, found, repair := tool.Extract(raw)
	if !found || repair != "" || call.Slots["cmd"] != cmd {
		t.Fatalf("raw executable command changed: %+v %q", call, repair)
	}
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Streaming = true
	m.Chat.Seq = 1
	for i := 0; i < len(raw); i += 7 {
		um, _ := m.Update(tokenMsg{seq: 1, delta: raw[i:min(i+7, len(raw))]})
		m = um.(Model)
		visible := m.renderChat(100, 50)
		if strings.Contains(visible, "SECRET_PAYLOAD") {
			t.Errorf("live onToken renderer leaked literal: %q", visible)
		}
	}
	if m.Chat.Stream.String() != raw {
		t.Fatal("display filtering changed raw stream")
	}
	dir := t.TempDir()
	sink := fanoutLog{logs: []agent.SessionLog{
		displayRawSession{dir}, progLog{seq: 1, send: func(msg tea.Msg) { um, _ := m.Update(msg); m = um.(Model) }},
	}}
	if err := sink.Append(context.Background(), "display", "reply", raw); err != nil {
		t.Fatal(err)
	}
	if err := sink.Append(context.Background(), "display", "tool", "bash command requested"); err != nil {
		t.Fatal(err)
	}
	if m.Chat.Stream.Len() != 0 || m.Chat.FlushedAll.String() != raw {
		t.Fatal("flush lost or rewrote raw evidence")
	}
	if len(m.Chat.Lines) != 2 || m.Chat.Lines[0] != (Line{lineAssistant, "before\r\nafter"}) || m.Chat.Lines[1].Kind != lineTool {
		t.Errorf("flush did not show prose/tool separately: %+v", m.Chat.Lines)
	}
	if visible := m.renderChat(100, 50); strings.Contains(visible, "SECRET_PAYLOAD") || !strings.Contains(visible, "before") || !strings.Contains(visible, "after") || !strings.Contains(visible, "bash command requested") {
		t.Errorf("flushed renderer visibility=%q", visible)
	}
	bytes, err := os.ReadFile(filepath.Join(dir, "display.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var event session.Event
	if err := json.Unmarshal([]byte(strings.Split(string(bytes), "\n")[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "reply" || event.Text != raw {
		t.Fatal("JSONL bytes do not retain exact reply")
	}
	events, err := session.Read(dir, "display")
	if err != nil || len(events) != 2 || events[0].Text != raw {
		t.Fatalf("raw session read=%+v err=%v", events, err)
	}
}

func TestToolDisplayMalformedRepairVisible(t *testing.T) {
	raw := "before\n```tool\ntool: bash\ncmd: <<BAD LABEL\n```\nSECRET_PAYLOAD\nBAD LABEL\n```"
	_, _, repair := tool.Extract(raw)
	if repair == "" {
		t.Fatal("malformed fixture did not cause real parser repair")
	}
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.Seq = 1
	m.onToken(tokenMsg{seq: 1, delta: raw})
	m.flushStream()
	m.onEvent(eventMsg{seq: 1, kind: "result", text: repair})
	if len(m.Chat.Lines) != 2 || m.Chat.Lines[0] != (Line{lineAssistant, "before"}) || m.Chat.Lines[1] != (Line{lineResult, repair}) {
		t.Errorf("hidden malformed span swallowed or obscured repair: %+v", m.Chat.Lines)
	}
	if m.Chat.FlushedAll.String() != raw {
		t.Fatal("malformed display dropped raw evidence")
	}
}

func TestToolDisplayMultipleRepairAndPartialEvidence(t *testing.T) {
	block := "```tool\ntool: bash\ncmd: <<END\n```\nSECRET_PAYLOAD\nEND\n```"
	for _, tc := range []struct{ name, raw string }{
		{"multiple", "before\n" + block + "\n" + block + "\nafter"},
		{"partial transport", block[:strings.LastIndex(block, "\nEND")]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testCfg(), "test")
			m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 1
			m.onToken(tokenMsg{seq: 1, delta: tc.raw})
			dir := t.TempDir()
			sink := fanoutLog{logs: []agent.SessionLog{displayRawSession{dir}, progLog{seq: 1, send: func(msg tea.Msg) { um, _ := m.Update(msg); m = um.(Model) }}}}
			kind, text := "reply", tc.raw
			if tc.name == "partial transport" {
				kind, text = "partial", "partial reply (not complete; not executed):\n"+tc.raw
			}
			if err := sink.Append(context.Background(), "preserved", kind, text); err != nil {
				t.Fatal(err)
			}
			if tc.name == "multiple" {
				call, found, repair := tool.Extract(tc.raw)
				if !found || repair == "" || call.Name != "" {
					t.Fatal("multiple-block control was not refused")
				}
				if err := sink.Append(context.Background(), "preserved", "result", repair); err != nil {
					t.Fatal(err)
				}
				m.flushStream()
				if len(m.Chat.Lines) != 2 || m.Chat.Lines[0] != (Line{lineAssistant, "before\nafter"}) || m.Chat.Lines[1] != (Line{lineResult, repair}) {
					t.Errorf("multiple repair/prose not visible: %+v", m.Chat.Lines)
				}
			} else {
				failure := "model error: unexpected EOF\npartial response usage: unknown (not reported by server)"
				if err := sink.Append(context.Background(), "preserved", "error", failure); err != nil {
					t.Fatal(err)
				}
				m.flushStream()
				if len(m.Chat.Lines) != 2 || m.Chat.Lines[0].Kind != lineSys || !strings.HasPrefix(m.Chat.Lines[0].Text, "partial reply (not complete; not executed):") || m.Chat.Lines[1] != (Line{lineSys, failure}) {
					t.Errorf("partial/error evidence labels were lost: %+v", m.Chat.Lines)
				}
			}
			events, err := session.Read(dir, "preserved")
			if err != nil || len(events) != 2 || events[0].Kind != kind || events[0].Text != text || m.Chat.FlushedAll.String() != tc.raw {
				t.Fatalf("display changed raw/partial session: %+v err=%v", events, err)
			}
			for _, l := range m.Chat.Lines {
				if l.Kind == lineAssistant && strings.Contains(l.Text, "SECRET_PAYLOAD") {
					t.Error("payload leaked as ordinary assistant prose")
				}
			}
		})
	}
}
