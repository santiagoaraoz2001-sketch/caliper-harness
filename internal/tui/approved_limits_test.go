package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"specific-harness/internal/llm"
	"strings"
	"testing"
	"time"
)

// Reflection keeps the final behavioral witness compilable on original owners;
// absent original config fields leave the original fixed behavior untouched.
func setApprovedLimit(m *Model, group, name string, n int) {
	v := reflect.ValueOf(&m.Cfg).Elem().FieldByName(group)
	if v.IsValid() {
		v = v.FieldByName(name)
		if v.IsValid() {
			v.SetInt(int64(n))
		}
	}
}
func TestApprovedViewLimitsActualOwners(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for i := 0; i < 4; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("one\ntwo\nthree\nfour\nfive\n"), 0600)
	}
	m := New(testCfg(), "test")
	setApprovedLimit(&m, "UI", "MaxFileEntries", 2)
	setApprovedLimit(&m, "UI", "MaxFileViewLines", 3)
	list := m.refreshFiles()
	setApprovedLimit(&m, "UI", "MaxFileEntries", 4)
	msg := list().(filesLoadedMsg)
	if len(msg.files) != 2 || !msg.truncated {
		t.Fatalf("configured captured list=%+v", msg)
	}
	m.onFilesLoaded(msg)
	if !strings.Contains(strings.Join(m.renderFileList(), "\n"), "first 2 shown") {
		t.Error("footer must describe delivered count, not changed config")
	}
	read := m.openFile("f0")
	setApprovedLimit(&m, "UI", "MaxFileViewLines", 5)
	opened := read().(filesOpenedMsg)
	if len(opened.lines) != 4 || opened.lines[2] != "three" || !strings.Contains(opened.lines[3], "capped") {
		t.Fatalf("configured captured read=%q", opened.lines)
	}
	m = decisionsModel(t)
	setApprovedLimit(&m, "UI", "MaxDecisionRows", 2)
	cmd := m.refreshDecisions()
	setApprovedLimit(&m, "UI", "MaxDecisionRows", 4)
	m = loadApprovedDecisions(m, cmd().(decisionsLoadedMsg))
	if len(m.Decisions.Rows) != 2 {
		t.Fatalf("rows=%d", len(m.Decisions.Rows))
	}
	setApprovedLimit(&m, "UI", "MaxRunIDs", 2)
	setApprovedLimit(&m, "UI", "MaxAlerts", 2)
	setApprovedLimit(&m, "UI", "MaxLegacyMessages", 2)
	m.Chat.SessionIDs = nil
	for _, id := range []string{"first", "second", "third"} {
		m.noteSession(id)
		m.alert("info", id)
	}
	if strings.Join(m.Chat.SessionIDs, ",") != "second,third" || len(m.Alerts) != 2 || m.Alerts[0].Text != "second" {
		t.Fatalf("ids=%v alerts=%+v", m.Chat.SessionIDs, m.Alerts)
	}
	m.Chat.History = []llm.Message{{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}, {Role: "user", Content: "three"}}
	m.Chat.Running = true
	m.onRunDone(runDoneMsg{seq: m.Chat.Seq, final: "four"})
	if len(m.Chat.History) != 2 {
		t.Fatalf("legacy messages=%d", len(m.Chat.History))
	}
}
func loadApprovedDecisions(m Model, msg decisionsLoadedMsg) Model { m.onDecisionsLoaded(msg); return m }

// Title captures its configured deadline before async work; this server is test-owned.
func TestApprovedTitleTimeoutPath(t *testing.T) {
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(1500 * time.Millisecond):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"wrong title"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	m := New(testCfg(), "test")
	m.Server = srv.URL
	m.ModelName = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Turns = 1
	setApprovedLimit(&m, "Tabs", "TitleTimeoutSeconds", 1)
	setApprovedLimit(&m, "Model", "PlainRequestTimeoutSeconds", 4)
	cmd := m.titleTurn("done")
	setApprovedLimit(&m, "Tabs", "TitleTimeoutSeconds", 4)
	if msg := cmd(); msg != nil {
		t.Fatalf("title request did not time out: %+v", msg)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("actual title cancellation not observed")
	}
}
