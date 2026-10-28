package tui

import (
	"github.com/charmbracelet/x/ansi"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"strings"
	"testing"
)

func footerField(t *testing.T, m Model, section, keyName string) Model {
	t.Helper()
	m.navigate(ViewSettings, "")
	m.Input.SetValue("")
	for i, f := range settingFields() {
		if f.Section == section && f.Key == keyName {
			m.Settings.Sel = i
			return m
		}
	}
	t.Fatal("field missing")
	return m
}
func assertFooterCfg(t *testing.T, m Model) {
	t.Helper()
	if m.St.Auth != ShortAuth(m.Cfg.Auth.Mode) || m.St.Think != m.Cfg.Params.Thinking || m.St.Model != m.Cfg.Model.Name || m.ModelName != m.Cfg.Model.Name || m.St.CtxWindow != m.Cfg.Context.Window {
		t.Fatalf("derived drift %+v cfg=%+v", m.St, m.Cfg)
	}
	s := ansi.Strip(m.footer())
	think := "OFF requested"
	if m.Cfg.Params.Thinking {
		think = "ON requested"
	}
	if !strings.Contains(s, ShortAuth(m.Cfg.Auth.Mode)) || !strings.Contains(s, think) || !strings.Contains(s, m.Cfg.Model.Name) {
		t.Fatalf("footer %q", s)
	}
}
func TestR13SettingsKeysSaveReopenFooter(t *testing.T) {
	m := settingsModel(t)
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "original context", false)
	before := m.Chat.Context.Clone()
	for range 3 {
		m = footerField(t, m, "auth", "mode")
		m = key(m, "enter")
		assertFooterCfg(t, m)
	}
	for range 2 {
		m = footerField(t, m, "params", "thinking")
		m = key(m, "right")
		assertFooterCfg(t, m)
	}
	for _, f := range []struct{ section, name, value string }{{"model", "name", "footer-model"}, {"context", "window", "32768"}} {
		m = footerField(t, m, f.section, f.name)
		m = key(m, "enter")
		m.Input.SetValue(f.value)
		m = key(m, "enter")
		assertFooterCfg(t, m)
	}
	if !reflect.DeepEqual(before, m.Chat.Context) {
		t.Fatal("settings rewrote chat context")
	}
	m = footerField(t, m, "auth", "mode")
	m = key(m, "enter")
	m = footerField(t, m, "params", "thinking")
	m = key(m, "enter")
	m = key(m, "s")
	cfg, used, e := config.Load(m.CfgPath)
	if e != nil || !used {
		t.Fatal(e)
	}
	reopened := New(cfg, "test")
	assertFooterCfg(t, reopened)
	if !reflect.DeepEqual(before, m.Chat.Context) {
		t.Fatal("save rewrote chat")
	}
}
func TestR13NextEscapeActualPriority(t *testing.T) {
	t.Run("status-alert-clear", func(t *testing.T) {
		m := New(testCfg(), "test")
		m.navigate(ViewManage, "")
		m.alert("warn", "owned notice")
		if s := ansi.Strip(m.renderServe(120, 25)); !strings.Contains(s, "esc dismisses alert") || strings.Contains(s, "esc chat") {
			t.Fatalf("status promise %q", s)
		}
		if !strings.Contains(ansi.Strip(m.footer()), "esc dismisses alert") {
			t.Fatal("footer priority")
		}
		m = key(m, "esc")
		if !m.serveActive() || !m.alertHidden {
			t.Fatal("alert action")
		}
		if !strings.Contains(ansi.Strip(m.footer()), "esc chat") {
			t.Fatal("clear promise")
		}
		m = key(m, "esc")
		if m.Tabs[m.Active].at.view != ViewChat {
			t.Fatal("return action")
		}
	})
	t.Run("help-over-dialog", func(t *testing.T) {
		m := New(testCfg(), "test")
		m.navigate(ViewManage, "")
		m.Chat.Dialog = &Dialog{Kind: "auth", Answer: make(chan string, 1)}
		m = submit(m, "/help")
		if !strings.Contains(ansi.Strip(m.View()), "closes") {
			t.Fatal("help hint")
		}
		m = key(m, "esc")
		if m.HelpOpen || m.Chat.Dialog == nil {
			t.Fatal("help did not own Escape")
		}
		if !strings.Contains(ansi.Strip(m.footer()), "esc declines") {
			t.Fatal("dialog promise")
		}
		m = key(m, "esc")
		if m.Chat.Dialog != nil {
			t.Fatal("dialog not declined")
		}
	})
	t.Run("ask-comment-before-alert", func(t *testing.T) {
		m := New(testCfg(), "test")
		m.navigate(ViewManage, "")
		m.alert("warn", "owned")
		m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "pick", Options: []string{"one"}}}, Commenting: true, AskDone: make(chan []ask.Answer, 1)}
		if !strings.Contains(ansi.Strip(m.footer()), "esc leaves comment") {
			t.Fatal("comment promise")
		}
		m = key(m, "esc")
		if m.Chat.Dialog == nil || m.Chat.Dialog.Commenting || len(m.Alerts) != 1 {
			t.Fatal("comment action")
		}
		if !strings.Contains(ansi.Strip(m.footer()), "esc skips all questions") {
			t.Fatal("question promise")
		}
		m = key(m, "esc")
		if m.Chat.Dialog != nil || len(m.Alerts) != 1 {
			t.Fatal("question action")
		}
	})
	t.Run("edit-before-alert", func(t *testing.T) {
		m := footerField(t, settingsModel(t), "model", "name")
		m = key(m, "enter")
		if !strings.Contains(ansi.Strip(m.footer()), "esc cancels edit") {
			t.Fatal("edit promise")
		}
		m = key(m, "esc")
		if m.Settings.Editing != nil {
			t.Fatal("edit action")
		}
		if !strings.Contains(ansi.Strip(m.footer()), "esc dismisses alert") {
			t.Fatal("alert after edit")
		}
	})
	t.Run("picker-before-alert", func(t *testing.T) {
		m := New(testCfg(), "test")
		m.navigate(ViewManage, "")
		m.alert("warn", "owned")
		m.Input.SetValue("/sta")
		if !strings.Contains(ansi.Strip(m.footer()), "esc closes menu") {
			t.Fatal("picker promise")
		}
		m = key(m, "esc")
		if len(m.suggestions()) != 0 || len(m.Alerts) != 1 {
			t.Fatal("picker action")
		}
	})
}
func TestR13NonchatCurrentGuidance(t *testing.T) {
	m := New(testCfg(), "test")
	m.navigate(ViewManage, "")
	m = submit(m, "ordinary text")
	s := lastAlert(m).Text
	if strings.Contains(s, "lands in") || strings.Contains(s, "T6.") || !strings.Contains(s, "/chat") || !strings.Contains(s, "esc dismisses alert") {
		t.Fatalf("guidance %q", s)
	}
	m = key(m, "esc")
	if !m.serveActive() {
		t.Fatal("guidance action")
	}
	m = submit(m, "/chat")
	if m.Tabs[m.Active].at.view != ViewChat {
		t.Fatal("advertised route")
	}
}

func TestR13RunAndDetailEscapePriority(t *testing.T) {
	m := New(testCfg(), "test")
	m.navigate(ViewManage, "")
	cancelled := false
	m.Chat.Running = true
	m.Chat.cancel = func() { cancelled = true }
	m = submit(m, "ordinary text")
	if !strings.Contains(lastAlert(m).Text, "esc stops run") || !strings.Contains(ansi.Strip(m.footer()), "esc stops run") {
		t.Fatal("run priority")
	}
	m = key(m, "esc")
	if !cancelled || !m.Chat.stopFlag || len(m.Alerts) == 0 {
		t.Fatal("run action")
	}
	m = New(testCfg(), "test")
	m.navigate(ViewFiles, "")
	m.Files.Path = "owned fixture"
	m.alert("warn", "notice")
	if !strings.Contains(ansi.Strip(m.footer()), "esc closes detail") {
		t.Fatal("detail priority")
	}
	m = key(m, "esc")
	if m.Files.Path != "" || len(m.Alerts) != 1 || !m.filesActive() {
		t.Fatal("detail action")
	}
}
