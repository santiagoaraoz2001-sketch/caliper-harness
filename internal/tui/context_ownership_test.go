package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"specific-harness/internal/llm"
)

func TestContextTitleOriginRevisionAndConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("runtime capture path%s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "Origin Title"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	m := New(testCfg(), "test")
	m.Server = srv.URL + "/v1"
	m.ModelName = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Turns = 1
	m.Chat.LastFull = "original"
	cmd := m.titleTurn("final")
	m.newTab()
	msg := cmd().(titleMsg)
	m.onTitle(msg)
	if m.Tabs[0].Title != "Origin Title" || m.Tabs[1].Title != "" {
		t.Fatalf("foreign title landed:%+v", m.Tabs)
	}
	m.switchTab(0)
	m.newChat()
	m.onTitle(msg)
	if m.Tabs[0].Title != "Origin Title" {
		t.Fatal("stale rename reached replacement")
	}
	fresh := m.origin()
	m.Cfg.Params.Thinking = !m.Cfg.Params.Thinking
	m.onTitle(titleMsg{title: "MUST_NOT_APPLY", origin: fresh})
	if m.Tabs[0].Title == "MUST_NOT_APPLY" {
		t.Fatal("stale config epoch title applied")
	}
}
func TestManualCompactionCancellationHoldsOwnerAndNextRun(t *testing.T) {
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	m := transportCompactModel(t, srv.URL+"/v1")
	m.St.CtxWindow = 32768
	m.Server = srv.URL + "/v1"
	m.ModelName = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Templates.Path = "../../templates"
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "ORIGINAL", false)
	m.Chat.Context.Add("history", "assistant", "consumed work", false)
	before := m.Chat.Context.Clone()
	cmd := m.compactChat()
	if cmd == nil || !m.Chat.Compacting {
		t.Fatal("manual slot not admitted")
	}
	if again := m.compactChat(); again != nil {
		t.Fatal("duplicate manual inference admitted")
	}
	done := make(chan compactDoneMsg, 1)
	go func() { done <- cmd().(compactDoneMsg) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owned HTTP not entered")
	}
	m.newTab()
	m.switchTab(0)
	ownerID := m.Chat.ID
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = updated.(Model)
	if len(m.Tabs) != 2 || m.Chat.ID != ownerID || !m.Chat.Compacting {
		t.Fatal("pending compaction owner was closed")
	}
	if len(m.Alerts) == 0 || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "stop compaction") {
		t.Fatal("pending close refusal lacked recovery guidance")
	}
	m.switchTab(1)
	m.stopChat()
	if !m.compactBusy(false) {
		t.Fatal("busy released before actual HTTP settlement")
	}
	select {
	case msg := <-done:
		if msg.runErr == nil {
			t.Fatal("cancellation became success")
		}
		m.onCompactDone(msg)
	case <-time.After(3 * time.Second):
		t.Fatal("owned cancellation did not settle")
	}
	if m.compactBusy(false) || len(m.Chat.Context.Entries) != 0 || m.Tabs[0].chat.Context.Entries[0].Message.Content != before.Entries[0].Message.Content {
		t.Fatal("owner cleanup/foreign history/context intact violated")
	}
	m.switchTab(0)
	if m.Chat.Context.Measured {
		t.Fatal("manual cancellation invented a request measurement")
	}
	// A completed control is origin-bound; edits make stale payloads inapplicable.
	o := m.origin()
	m.Chat.Context.Invalidate()
	m.onCompactDone(compactDoneMsg{origin: o, continuation: session.Continuation{Version: 1}, summary: "BAD"})
	if len(m.Chat.Context.Entries) != 2 {
		t.Fatal("stale revision replaced context")
	}
}
func TestFooterMeasurementSeparateFromCumulativeCost(t *testing.T) {
	m := New(testCfg(), "test")
	m.Chat.Running = true
	m.Chat.LastFull = "q"
	c := session.Continuation{Version: 1, Measured: true, Prompt: 200, ModelKey: m.contextKey()}
	c.Add("intent", "user", "q", false)
	m.onRunDone(runDoneMsg{seq: m.Chat.Seq, final: "a", in: 300, out: 50, continuation: c})
	if !strings.Contains(m.footer(), "200/") || strings.Contains(m.footer(), "350/") {
		t.Fatalf("footer ledger substituted%s", m.footer())
	}
	m.Chat.Context.Invalidate()
	if !strings.Contains(m.footer(), "unknown/") {
		t.Fatal("edit kept old measured reading")
	}
	c = m.Chat.Context
	c.Measured = true
	m.Chat.Context = c
	m.Cfg.Model.Runtime = "changed"
	if !strings.Contains(m.footer(), "unknown/") {
		t.Fatal("runtime change kept reading")
	}
}

func TestManualCompactionOriginPersistenceAndClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"COMPACTED\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":600}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	cfg := testCfg()
	cfg.Templates.Path = "../../templates"
	dir := t.TempDir()
	cfg.Store.Path = dir + "/kuzu"
	cfg.Sessions.Path = dir + "/sessions"
	cfg.Decision = transportCompactModel(t, srv.URL+"/v1").Cfg.Decision
	m := New(cfg, "test")
	m.St.CtxWindow = 32768
	m.Server = srv.URL + "/v1"
	m.ModelName = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "ORIGINAL", false)
	m.Chat.Context.Add("history", "assistant", "DISCARDED"+strings.Repeat(" old consumed draft", 100), false)
	m.Chat.Context.Add("failure", "user", "actual check exit5", false)
	m.Chat.Context.Add("note", "user", "By the way: NOTE", false)
	cmd := m.compactChat()
	if cmd == nil {
		t.Fatal(m.Alerts)
	}
	message := cmd().(compactDoneMsg)
	if message.runErr != nil {
		t.Fatal(message.runErr)
	}
	m.newTab()
	save := m.onCompactDone(message)
	if save == nil || !m.compactBusy(false) {
		t.Fatal("persistence did not retain busy owner")
	}
	saved := save().(compactDoneMsg)
	if saved.runErr != nil {
		t.Fatal(saved.runErr)
	}
	m.onCompactDone(saved)
	if m.compactBusy(false) || len(m.Chat.Context.Entries) != 0 {
		t.Fatal("settlement leaked busy/foreign state")
	}
	origin := m.Tabs[0].chat
	applied := ""
	for _, e := range origin.Context.Entries {
		applied += e.Message.Content
	}
	if !strings.Contains(applied, "COMPACTED") || strings.Contains(applied, "DISCARDED") {
		t.Fatalf("persisted success not applied to owner context: %s; alerts%v", applied, m.Alerts)
	}
	if origin.LastIn != 120 || origin.LastOut != 600 || origin.Context.Measured {
		t.Fatal("cost/reading conflated or double charged")
	}
	events, err := session.Read(cfg.Sessions.Path, origin.SessionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore(events, 1000)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, v := range restored.Messages() {
		text += v.Content
	}
	for _, v := range []string{"ORIGINAL", "COMPACTED", "actual check exit5", "NOTE"} {
		if !strings.Contains(text, v) {
			t.Errorf("persisted fact lost%q", v)
		}
	}
	if strings.Contains(text, "DISCARDED") {
		t.Fatal("discarded turn persisted")
	}
	// Reopen the actual native store after completion: the invocation lease is closed.
	s, err := store.Open(cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestManualCompactionConfigAfterAppendKeepsUIAndResetsResumeBasis(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"SAVED_OLD_RUNTIME\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	cfg := testCfg()
	cfg.Templates.Path = "../../templates"
	dir := t.TempDir()
	cfg.Store.Path = dir + "/kuzu"
	cfg.Sessions.Path = dir + "/sessions"
	cfg.Decision = transportCompactModel(t, srv.URL+"/v1").Cfg.Decision
	m := New(cfg, "test")
	m.Server = srv.URL + "/v1"
	m.ModelName = "old"
	m.Cfg.Model.Runtime = "openai"
	m.Chat.Context = session.Continuation{Version: 1, BasisTokens: 100, BasisChars: 1000, ModelKey: m.contextKey()}
	m.Chat.Context.Add("intent", "user", "ORIGINAL", false)
	m.Chat.Context.Add("reply", "assistant", "DISCARDED"+strings.Repeat(" old consumed draft", 100), false)
	before := m.Chat.Context.Clone()
	cmd := m.compactChat()
	if cmd == nil {
		t.Fatal(m.Alerts)
	}
	save := m.onCompactDone(cmd().(compactDoneMsg))
	if save == nil {
		t.Fatal(m.Alerts)
	}
	saved := save().(compactDoneMsg)
	if saved.runErr != nil {
		t.Fatal(saved.runErr)
	}
	m.ModelName = "changed-after-append"
	m.onCompactDone(saved)
	if m.Chat.Compacting || m.Chat.Context.Entries[1].Message.Content != before.Entries[1].Message.Content {
		t.Fatal("stale persisted result applied/busy leaked")
	}
	events, err := session.Read(cfg.Sessions.Path, m.Chat.SessionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore(events, 10000, m.contextKey())
	if err != nil {
		t.Fatal(err)
	}
	if restored.BasisChars != 0 || restored.BasisTokens != 0 || restored.Measured {
		t.Fatal("changed runtime inherited old calibration")
	}
	found := false
	for _, e := range restored.Entries {
		found = found || strings.Contains(e.Message.Content, "SAVED_OLD_RUNTIME")
	}
	if !found {
		t.Fatal("append was silently rolled back/hidden")
	}
}

func TestContextStaleTitleAfterActualRunSettlement(t *testing.T) {
	m := New(testCfg(), "test")
	m.Cfg.Tabs.TitleEvery = 0
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "ORIGINAL", false)
	// Ordinary navigation preserves the active tab's old backing copy.
	m.newTab()
	m.switchTab(0)
	old := m.origin()
	m.Tabs[0].Title = "KEPT"
	next := m.Chat.Context.Clone()
	next.Add("task", "user", "NEW_TASK", false)
	next.Invalidate()
	m.Chat.Running = true
	m.Chat.LastFull = "NEW_TASK"
	updated, _ := m.Update(runDoneMsg{seq: m.Chat.Seq, final: "NEW_REPLY", continuation: next})
	m = updated.(Model)
	if m.Chat.Context.Revision == old.revision || m.Tabs[0].chat.Context.Revision != old.revision {
		t.Fatal("actual settlement/backing-copy calibration failed")
	}
	updated, _ = m.Update(titleMsg{title: "STALE_PREVIOUS_TURN", origin: old})
	m = updated.(Model)
	if m.Tabs[0].Title != "KEPT" {
		t.Fatalf("stale title renamed revised conversation: %q", m.Tabs[0].Title)
	}
	// A title actually earned by the new settlement must still land, even
	// when its owning chat is moved into the nonactive tab backing copy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "Earned Title"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	m.Server, m.ModelName = srv.URL+"/v1", "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Tabs.TitleEvery = 1
	next = m.Chat.Context.Clone()
	next.Add("task", "user", "LATEST_TASK", false)
	next.Invalidate()
	m.Chat.Running = true
	m.Chat.LastFull = "LATEST_TASK"
	updated, cmd := m.Update(runDoneMsg{seq: m.Chat.Seq, final: "LATEST_REPLY", continuation: next})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("ordinary settlement failed to earn a title")
	}
	title := cmd().(titleMsg)
	if title.origin != m.origin() {
		t.Fatal("earned title captured pre-settlement origin")
	}
	updated, _ = m.Update(title)
	m = updated.(Model)
	if m.Tabs[0].Title != "Earned Title" {
		t.Fatal("fresh active title refused")
	}
	m.Tabs[0].Title = "KEPT_AGAIN"
	m.newTab()
	updated, _ = m.Update(title)
	m = updated.(Model)
	if m.Tabs[0].Title != "Earned Title" || m.Tabs[1].Title != "" {
		t.Fatal("fresh same-ID inactive title lost or crossed tabs")
	}
}

func TestDeferredTitleClosedOriginCannotRenameRecreatedTab(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string        `json:"model"`
			Messages []llm.Message `json:"messages"`
			Tools    []any         `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/chat/completions" || request.Model != "title-fixture" || len(request.Tools) != 0 || len(request.Messages) != 2 || !strings.HasPrefix(request.Messages[0].Content, "Name this conversation") {
			t.Error("deferred fixture was not the actual no-tools title request")
		}
		entered <- struct{}{}
		select {
		case <-release:
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "CLOSED ORIGIN TITLE"}, "finish_reason": "stop"}}})
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cfg := testCfg()
	cfg.Model.Server, cfg.Model.Name, cfg.Model.Runtime = srv.URL+"/v1", "title-fixture", "openai"
	m := New(cfg, "test")
	m.Server, m.ModelName = cfg.Model.Server, cfg.Model.Name
	m.Cfg.Tabs.TitleEvery = 1
	m.Chat.Turns, m.Chat.LastFull = 1, "ORIGINAL A"
	m.Chat.History = []llm.Message{{Role: "user", Content: "A history"}}
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "A intent", false)
	m.Tabs[0].Title = "A"
	owner := m.origin()
	cmd := m.titleTurn("A final")
	if cmd == nil {
		t.Fatal("title not admitted")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("title HTTP did not enter")
	}
	// B survives; close A through actual key routing, then create independent C.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	m.Tabs[m.Active].Title = "B KEPT"
	m.Chat.History = []llm.Message{{Role: "user", Content: "B Unicode ρ\nsecond line"}}
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "B intent", false)
	m.Chat.appendLine(lineUser, "B transcript")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = updated.(Model)
	if len(m.Tabs) != 1 || m.Chat.ID == owner.id {
		t.Fatal("owning A did not actually close")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	m.Tabs[m.Active].Title = "C KEPT"
	m.Chat.History = []llm.Message{{Role: "assistant", Content: "C independent"}}
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "C intent", false)
	m.Chat.appendLine(lineUser, "C transcript")
	state := func() []byte {
		m.SyncTabChat()
		var values []any
		for _, tab := range m.Tabs {
			if tab.chat.ID == owner.id {
				t.Fatal("closed owner survived in recreated tabs")
			}
			values = append(values, []any{tab.chat.ID, tab.Title, tab.chat.History, tab.chat.Context, tab.chat.Lines})
		}
		b, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := string(state())
	close(release)
	select {
	case raw := <-result:
		msg, ok := raw.(titleMsg)
		if !ok || msg.origin != owner || msg.title != "CLOSED ORIGIN TITLE" {
			t.Fatalf("actual deferred result lost original owner: %#v", raw)
		}
		updated, _ = m.Update(msg)
		m = updated.(Model)
	case <-time.After(3 * time.Second):
		t.Fatal("released title did not settle")
	}
	if after := string(state()); after != before {
		t.Fatalf("closed title changed B/C: before=%s after=%s", before, after)
	}
}
