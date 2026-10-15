package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func transportCompactModel(t *testing.T, endpoint string) Model {
	t.Helper()
	cfg := testCfg()
	dir := t.TempDir()
	cfg.Store.Path, cfg.Sessions.Path = dir+"/kuzu", dir+"/sessions"
	cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
	selection := decision.SelectionFor(cfg.Decision)
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: selection}, "backends": []string{"cpu"}})
			return
		}
		var q decision.Request
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/judge" || len(q.Options) != 2 {
			t.Errorf("unexpected compaction judgment: %s %+v", r.URL.Path, q)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": q.Options[0], "confidence": 1, "model": "component", "device": "cpu"})
	}))
	t.Cleanup(judge.Close)
	cfg.Decision.Server = judge.URL
	m := New(cfg, "test")
	m.Server = endpoint
	m.ModelName = "fixture"
	m.Cfg.Model.Server = endpoint
	m.Cfg.Model.Name = "fixture"
	m.Cfg.Model.Runtime = "openai"
	m.Cfg.Templates.Path = "../../templates"
	m.St.CtxWindow = 32768
	m.Chat.Context = session.Continuation{Version: 1}
	m.Chat.Context.Add("intent", "user", "ORIGINAL TASK", false)
	m.Chat.Context.Add("reply", "assistant", strings.Repeat("consumed unverified proposal ", 200), false)
	m.Chat.Context.ModelKey = m.contextKey()
	if m.Server != endpoint || m.Cfg.Model.Server != endpoint || m.ModelName != "fixture" || m.Cfg.Model.Name != "fixture" {
		t.Fatal("unbound transport fixture")
	}
	return m
}

// Public command + actual HTTP transport/Kuzu/session publication + subsequent
// Runner request. The model and D1 replies are controlled, not native inference.
func TestManualCompactionPublicCommandCapacityTargetPublishAndContinuedRun(t *testing.T) {
	summary := "Work remains pending. Preserve the original request and the exact user comment. The recorded write receipt names main.go and hash actual; it does not establish correctness. The check exited7, and independent review remains missing. Historical model drafts are unverified proposals. Current source and settled tool associations stay available separately. Continue from the current files, inspect the relevant source before any edit, then execute the approved behavioral check and configured project check. Report their actual results, retain failures and denied operations, and request a real independent review when the checks justify it. No summary statement grants approval, changes task ownership, or records a PASS."
	var requests [][]llm.Message
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDefinition
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body.Messages)
		text := summary
		if len(requests) == 1 {
			if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || len(body.Tools) != 0 {
				t.Error("summary roles/tools changed")
			}
		} else {
			text = "Continued; correctness remains unverified."
		}
		raw, _ := json.Marshal(text)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":50}}\n\ndata: [DONE]\n\n", raw)
	}))
	defer model.Close()
	m := transportCompactModel(t, model.URL)
	m.Cfg.Context.Summary, m.Cfg.Context.SummaryMax, m.Cfg.Context.SummaryWordsPerToken = .1, .1, .5
	m.Chat.Context.Add("note", "user", "USER COMMENT: preserve κ exactly", false)
	m.Chat.Context.Add("failure", "user", "Actual check exited7; no current PASS", false)
	m.Chat.Context.Add("evidence", "user", "Write receipt main.go hash=actual; review missing", false)
	original := m.Chat.Context.Clone()
	id := session.NewID()
	m.noteSession(id)
	raw, err := original.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Append(m.SessionsPath, id, session.Event{Kind: "continuation", Text: raw}); err != nil {
		t.Fatal(err)
	}
	cmd := m.submit("/compact")
	if cmd == nil {
		t.Fatal("public compact command did not launch", m.Alerts)
	}
	done := cmd().(compactDoneMsg)
	if done.runErr != nil || done.summary != summary || done.continuation.Estimate() >= original.Estimate() {
		t.Fatal("valid capacity-fitting summary refused/not smaller", done.runErr)
	}
	save := m.onCompactDone(done)
	if save == nil {
		t.Fatal("summary not admitted to durable publication", m.Alerts)
	}
	saved := save().(compactDoneMsg)
	if saved.runErr != nil {
		t.Fatal(saved.runErr)
	}
	m.onCompactDone(saved)
	events, err := session.Read(m.SessionsPath, id)
	if err != nil {
		t.Fatal(err)
	}
	compacts, snapshots, sourceFound := 0, 0, false
	for _, event := range events {
		if event.Kind == "compact" {
			compacts++
		}
		if event.Kind == "continuation" {
			snapshots++
			sourceFound = sourceFound || event.Text == raw
		}
	}
	if compacts != 1 || snapshots != 2 || !sourceFound || m.Chat.Compacting || m.Chat.Context.Compactions != 1 {
		t.Fatalf("durable/raw/origin mismatch: compacts=%d snapshots=%d source=%t", compacts, snapshots, sourceFound)
	}
	restored, _, err := session.Restore(events, m.St.CtxWindow)
	if err != nil || !reflect.DeepEqual(restored.Entries, m.Chat.Context.Entries) {
		t.Fatal("published snapshot did not resume exactly", err)
	}
	pc, err := m.Cfg.LoadPrompts()
	if err != nil {
		t.Fatal(err)
	}
	r := agent.Runner{Prompts: pc, Model: llm.New(model.URL, "fixture"), ModelKey: m.contextKey(), Registry: tool.NewRegistry(pc), MaxSteps: 1, MaxTokens: m.St.CtxWindow, Continuation: restored}
	final, _, err := r.Run(context.Background(), "Continue this work.")
	if err != nil || final != "Continued; correctness remains unverified." || len(requests) != 2 {
		t.Fatalf("continued runner failed: requests=%d final=%q err=%v", len(requests), final, err)
	}
	joined := ""
	for _, msg := range requests[1] {
		joined += msg.Content + "\n"
	}
	for _, value := range []string{summary, "ORIGINAL TASK", "USER COMMENT: preserve κ exactly", "Actual check exited7; no current PASS", "Write receipt main.go hash=actual; review missing"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("continued wire lost %q", value)
		}
	}
}

func TestManualCompactionTransportBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Pending work.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Pending work."},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	for _, seconds := range []int{0, 60} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			m := transportCompactModel(t, srv.URL)
			m.Cfg.Agent.MaxRunSeconds = 0
			if seconds > 0 {
				m.Cfg.Agent.MaxRunSeconds = 60
			}
			cmd := m.compactChat()
			if cmd == nil {
				t.Fatal(m.Alerts)
			}
			defer m.Chat.compactCancel()
			done := cmd().(compactDoneMsg)
			deadline, has := done.ctx.Deadline()
			if has != (seconds > 0) || has && time.Until(deadline) < 50*time.Second {
				t.Errorf("manual budget=%d has deadline=%t remaining=%s", seconds, has, time.Until(deadline))
			}
			if done.runErr != nil {
				t.Fatal(done.runErr)
			}
		})
	}
}

func TestManualCompactionTransportConfiguredTimeoutAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&map[string]any{})
				entered <- struct{}{}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"progress\"}}]}\n\n")
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer srv.Close()
			m := transportCompactModel(t, srv.URL)
			m.Cfg.Agent.MaxRunSeconds = 1
			original := m.Chat.Context.Clone()
			cmd := m.compactChat()
			if cmd == nil {
				t.Fatal(m.Alerts)
			}
			defer m.Chat.compactCancel()
			done := make(chan compactDoneMsg, 1)
			go func() { done <- cmd().(compactDoneMsg) }()
			<-entered
			if cancelled {
				m.Chat.compactCancel()
			}
			var result compactDoneMsg
			select {
			case result = <-done:
			case <-time.After(2 * time.Second):
				m.Chat.compactCancel()
				result = <-done
				t.Error("configured/caller timeout did not stop progress")
			}
			if result.runErr == nil || !reflect.DeepEqual(result.continuation, original) {
				t.Fatal("timeout/cancel replaced continuation")
			}
			if save := m.onCompactDone(result); save != nil || m.Chat.Compacting || !reflect.DeepEqual(m.Chat.Context, original) {
				t.Fatal("failure persisted or owner/context not preserved")
			}
		})
	}
}

// Selected HTTP Judge plus actual temporary Kuzu/session owner; not native inference.
func TestManualCompactionSelectedJudgeOriginalSession(t *testing.T) {
	for _, disposition := range []string{"accept", "reject", "uncertain", "stale", "cancel"} {
		t.Run(disposition, func(t *testing.T) {
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				fmt.Fprint(w, "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"Actual check passed; failure remains.\",\"reasoning\":\"raw κ\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
			}))
			defer model.Close()
			m := transportCompactModel(t, model.URL)
			selection := decision.SelectionFor(m.Cfg.Decision)
			entered := make(chan struct{}, 1)
			calls := 0
			judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: selection}, "backends": []string{"cpu"}})
					return
				}
				var q struct {
					decision.Request
					Expected *decision.Selection
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path != "/judge" || q.Expected == nil || !reflect.DeepEqual(*q.Expected, selection) || !strings.Contains(q.Context, "Host configured check PASS") {
					t.Errorf("selected identity/full facts missing: %s %+v", r.URL.Path, q)
				}
				calls++
				if disposition == "cancel" {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				pick, confidence := 0, 1.0
				if disposition == "reject" {
					pick = 1
				}
				if disposition == "uncertain" {
					confidence = m.Cfg.Decision.RiskThreshold - .01
				}
				json.NewEncoder(w).Encode(map[string]any{"pick": pick, "option": q.Options[pick], "confidence": confidence, "model": "component", "device": "cpu"})
			}))
			defer judge.Close()
			m.Cfg.Decision.Server = judge.URL
			m.Chat.Context.Add("evidence", "user", "Host configured check PASS", false)
			m.Chat.Context.Add("failure", "user", "actual failure exit7", false)
			original := m.Chat.Context.Clone()
			id := session.NewID()
			m.noteSession(id)
			if err := session.Append(m.SessionsPath, id, session.Event{Kind: "prompt", Text: "ORIGINAL TASK"}); err != nil {
				t.Fatal(err)
			}
			cmd := m.compactChat()
			if cmd == nil {
				t.Fatal(m.Alerts)
			}
			if disposition == "stale" {
				m.ModelName = "changed-before-background"
			}
			var done compactDoneMsg
			if disposition == "cancel" {
				result := make(chan compactDoneMsg, 1)
				go func() { result <- cmd().(compactDoneMsg) }()
				select {
				case <-entered:
					m.Chat.compactCancel()
				case <-time.After(3 * time.Second):
					m.Chat.compactCancel()
					t.Fatal("selected judge not entered")
				}
				done = <-result
			} else {
				done = cmd().(compactDoneMsg)
			}
			if calls != 1 {
				t.Fatalf("selected judge calls=%d err=%v", calls, done.runErr)
			}
			if disposition == "accept" {
				if done.runErr != nil {
					t.Fatal(done.runErr)
				}
				m.newTab()
				save := m.onCompactDone(done)
				if save == nil {
					t.Fatal("accepted owner not durably settled")
				}
				saved := save().(compactDoneMsg)
				if saved.runErr != nil {
					t.Fatal(saved.runErr)
				}
				m.onCompactDone(saved)
				if len(m.Chat.Context.Entries) != 0 || m.Tabs[0].chat.Context.Compactions != 1 {
					t.Error("success applied to foreign tab or not origin")
				}
			} else {
				if save := m.onCompactDone(done); save != nil || !reflect.DeepEqual(m.Chat.Context, original) {
					t.Error("refused/stale/canceled result changed context")
				}
			}
			events, err := session.Read(m.SessionsPath, id)
			if err != nil {
				t.Fatal(err)
			}
			raw, judgments, compacts, snapshots := "", 0, 0, 0
			for _, e := range events {
				switch e.Kind {
				case "reasoning":
					if strings.HasPrefix(e.Text, "offset=") {
						_, p, _ := strings.Cut(e.Text, "\n")
						raw += p
					}
				case "judgment":
					judgments++
				case "compact":
					compacts++
				case "continuation":
					snapshots++
				}
			}
			if !strings.Contains(raw, "raw κ") || !strings.Contains(raw, "Actual check passed") {
				t.Error("original summary raw fields not in originating session")
			}
			if disposition != "cancel" && judgments != 1 {
				t.Errorf("judge receipt not in origin session: %d", judgments)
			}
			if disposition == "accept" {
				if compacts != 1 || snapshots != 1 {
					t.Error("successful deferred durable boundary missing")
				}
			} else if compacts != 0 || snapshots != 0 {
				t.Error("refused result recorded as accepted continuation")
			}
			if m.compactBusy(false) {
				t.Error("owned busy token leaked")
			}
		})
	}
}
