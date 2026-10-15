package tui

import (
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestToolsPublicSlashPreservesTaskAndHistory(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Context = session.Continuation{Version: 1, Entries: []session.Entry{{Message: llm.Message{Role: "user", Content: "intent"}, Kind: "intent"}}, Coding: &session.CodingState{TaskID: "owned", Workspace: "/workspace"}}
	m = submit(m, "/tools full")
	if m.Chat.Tools != "full" || m.Chat.Context.Coding.TaskID != "owned" || m.Chat.Context.Entries[0].Message.Content != "intent" {
		t.Fatal("view switched task/history")
	}
	for _, name := range agent.ToolNames("full") {
		if !strings.Contains(m.Chat.Lines[len(m.Chat.Lines)-1].Text, name) {
			t.Fatal("active names omit", name)
		}
	}
	if !strings.Contains(m.footer(), "tools: full") {
		t.Fatal("missing persistent indicator")
	}
	m.Chat.Running = true
	m = submit(m, "/tools coding")
	if m.Chat.Tools != "full" {
		t.Fatal("busy view switched")
	}
}
func TestCodingCheckSettingPreservesCommandBytes(t *testing.T) {
	m := New(testCfg(), "test")
	raw := "  printf 'x\\n'\ntrue  "
	f := SettingField{"coding", "check_command", "project check", "text"}
	m.applyField(f, raw)
	if m.Cfg.Coding.CheckCommand != raw || m.settingValue(f) != raw {
		t.Fatal("configured command changed")
	}
}
