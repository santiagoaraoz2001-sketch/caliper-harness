package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestSettingsGenerationBudgetArmedRefusalSave(t *testing.T) {
	sel := -1
	for i, f := range settingFields() {
		if f.Section == "params" && f.Key == "max_output_tokens" {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatal("output budget not offered by Settings")
	}
	m := settingsModel(t)
	fileBefore := finiteSettingsSave(t, m)
	m = submit(m, "/settings")
	m.Settings.Sel = sel
	for _, bad := range []string{"-1", "hot", "1.5", "999999999999999999999999999999"} {
		before := m.Cfg
		m = key(m, "enter")
		m = submit(m, bad)
		if !reflect.DeepEqual(m.Cfg, before) || !strings.Contains(lastAlert(m).Text, "params.max_output_tokens") {
			t.Fatalf("refusal changed config/unlabelled: %s", lastAlert(m).Text)
		}
		raw, _ := os.ReadFile(m.CfgPath)
		if string(raw) != string(fileBefore) {
			t.Fatal("refused edit altered saved file")
		}
	}
	m = key(m, "enter")
	m = submit(m, "2048")
	if m.settingValue(settingFields()[sel]) != "2048" {
		t.Fatal("valid budget did not apply")
	}
	m = key(m, "s")
	c, _, err := config.Load(m.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Cfg = c
	if m.settingValue(settingFields()[sel]) != "2048" {
		t.Fatal("budget lost on reopen")
	}
}

func TestSettingsGenerationBudgetTitleAndSummaryFactories(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		requests++
		if b["max_completion_tokens"] != float64(2048) {
			t.Errorf("auxiliary factory missing live cap: %v", b)
		}
		if b["stream"] == true {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Pending work\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			io.WriteString(w, `{"choices":[{"message":{"content":"Pending work"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	m := settingsModel(t)
	m.Cfg.Decision = transportCompactModel(t, srv.URL).Cfg.Decision
	m.Cfg.Params.MaxOutputTokens = 2048
	m.Server = srv.URL
	m.ModelName = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Turns = 1
	m.Chat.LastFull = "task"
	cmd := m.titleTurn("model claim")
	if cmd == nil {
		t.Fatal("title factory not invoked")
	}
	if msg := cmd().(titleMsg); msg.title == "" {
		t.Fatal("title failed")
	}
	m.Cfg.Templates.Path = "../../templates"
	m.St.CtxWindow = 32768
	m.Cfg.Context.Keep = .01
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "ORIGINAL", false)
	m.Chat.Context.Add("reply", "assistant", strings.Repeat("UNVERIFIED PROPOSAL ", 100), false)
	cmd = m.compactChat()
	if cmd == nil {
		t.Fatal(m.Alerts)
	}
	msg := cmd().(compactDoneMsg)
	if msg.runErr != nil {
		t.Fatal(msg.runErr)
	}
	if m.Chat.compactCancel != nil {
		m.Chat.compactCancel()
	}
	if requests != 2 {
		t.Fatalf("unexpected auxiliary requests %d", requests)
	}
}
