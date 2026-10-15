package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/session"
)

func TestToolDisplayUpdateSettlement(t *testing.T) {
	block := "```tool\ntool: bash\ncmd: <<END\n```\nHIDDEN_FINAL_PAYLOAD\nEND\n```"
	partialRaw := "before\n" + block[:strings.LastIndex(block, "\nEND")]
	label := "partial reply (not complete; not executed):\n"
	for _, tc := range []struct {
		name, stream, final, visible string
		failed                       bool
	}{
		{"partial error completion", partialRaw, label + partialRaw, label + "before", true},
		{"nonstream final", "", "before\n" + block + "\nafter", "before\nafter", false},
		{"ordinary prose", "", "before\n\nafter", "before\n\nafter", false},
		{"ordinary code", "", "before\n```python\nvisible = 'code'\n```\nafter", "before\n```python\nvisible = 'code'\n```\nafter", false},
		{"multiple final blocks", "", "before\n" + block + "\nbetween\n" + block + "\nafter", "before\nbetween\nafter", false},
		{"tool-only final", "", block, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(testCfg(), "settlement")
			m.Cfg.Tabs.TitleEvery = 0
			m.Chat.Running, m.Chat.Streaming, m.Chat.Seq = true, true, 1
			m.Chat.LastFull = "original user prompt"
			dir := t.TempDir()
			sink := fanoutLog{logs: []agent.SessionLog{displayRawSession{dir}, progLog{seq: 1, send: func(msg tea.Msg) {
				u, _ := m.Update(msg)
				m = u.(Model)
			}}}}
			if tc.stream != "" {
				u, _ := m.Update(tokenMsg{seq: 1, delta: tc.stream})
				m = u.(Model)
			}
			kind := "reply"
			var runErr error
			if tc.failed {
				kind = "partial"
				runErr = errors.New("unexpected EOF")
			}
			if err := sink.Append(context.Background(), "settlement", kind, tc.final); err != nil {
				t.Fatal(err)
			}
			if tc.failed {
				if err := sink.Append(context.Background(), "settlement", "error", "model error: unexpected EOF"); err != nil {
					t.Fatal(err)
				}
			}
			u, _ := m.Update(runDoneMsg{seq: 1, final: tc.final, runErr: runErr, in: 9, out: 7})
			m = u.(Model)
			var assistants []string
			for _, line := range m.Chat.Lines {
				if line.Kind == lineAssistant {
					assistants = append(assistants, line.Text)
					if strings.Contains(line.Text, "HIDDEN_FINAL_PAYLOAD") || strings.Contains(line.Text, "```tool") {
						t.Errorf("final tool payload leaked as ordinary assistant text: %q", line.Text)
					}
				}
			}
			if tc.visible == "" {
				if len(assistants) != 0 {
					t.Errorf("tool-only final produced assistant line: %q", assistants)
				}
			} else if len(assistants) == 0 || assistants[len(assistants)-1] != tc.visible {
				t.Errorf("completion visible=%q want final %q", assistants, tc.visible)
			}
			if m.Chat.Running || m.Chat.Streaming || m.Chat.Stream.Len() != 0 || m.Chat.FlushedAll.String() != tc.stream {
				t.Fatal("settlement rewrote raw stream or did not settle")
			}
			if len(m.Chat.History) != 2 || m.Chat.History[0].Content != "original user prompt" || m.Chat.History[1].Content != tc.final {
				t.Fatal("presentation changed raw history")
			}
			if m.Chat.LastIn != 9 || m.Chat.LastOut != 7 {
				t.Fatal("presentation changed usage")
			}
			events, err := session.Read(dir, "settlement")
			if err != nil || len(events) == 0 || events[0].Kind != kind || events[0].Text != tc.final {
				t.Fatalf("presentation changed raw session: %+v err=%v", events, err)
			}
			if tc.failed {
				if len(events) != 2 || events[1].Kind != "error" {
					t.Fatal("partial/error session shape changed")
				}
				var partialLabel, failureLabel bool
				for _, line := range m.Chat.Lines {
					partialLabel = partialLabel || strings.HasPrefix(line.Text, label)
					failureLabel = failureLabel || strings.Contains(line.Text, "run failed: unexpected EOF")
				}
				if !partialLabel || !failureLabel {
					t.Fatal("explicit partial/error labels lost at settlement")
				}
			}
		})
	}
}
