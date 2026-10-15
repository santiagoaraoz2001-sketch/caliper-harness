package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Excludes ChatTools deliberately, exercising the ordinary plain Model path.
type measurementPlainClient struct{ client *llm.Client }

func (m measurementPlainClient) Chat(ctx context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	return m.client.Chat(ctx, msgs)
}

func TestLivePromptRunnerEventsBeforeRunDone(t *testing.T) {
	for _, protocol := range []string{"plain", "text", "structured"} {
		t.Run(protocol, func(t *testing.T) {
			entered := make(chan int, 2)
			release := []chan struct{}{make(chan struct{}), make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["model"] != "measurement-fixture" || req.URL.Path != "/v1/chat/completions" {
					t.Error("fixture request identity", body, err, req.URL.Path)
				}
				requests++
				n := requests
				entered <- n
				select {
				case <-release[n-1]:
				case <-req.Context().Done():
					return
				}
				content := "FINAL"
				if n == 1 {
					content = "```tool\ntool: observe\ntext: observed\n```"
				}
				usage := map[string]int{"prompt_tokens": n * 100, "completion_tokens": 7}
				if protocol == "plain" {
					json.NewEncoder(w).Encode(map[string]any{"model": "measurement-fixture", "choices": []any{map[string]any{"message": llm.Message{Role: "assistant", Content: content}, "finish_reason": "stop"}}, "usage": usage})
					return
				}
				delta := map[string]any{"content": content}
				finish := "stop"
				if n == 1 && protocol == "structured" {
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "observed-call", "type": "function", "function": map[string]string{"name": "observe", "arguments": `{"text":"observed"}`}}}}
					finish = "tool_calls"
				}
				chunk, _ := json.Marshal(map[string]any{"model": "measurement-fixture", "choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}, "usage": usage})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer func() { cancel(); srv.Close() }()
			m := New(testCfg(), "measurement")
			m.Server, m.Cfg.Model.Server = srv.URL+"/v1", srv.URL+"/v1"
			m.ModelName, m.Cfg.Model.Name = "measurement-fixture", "measurement-fixture"
			m.Cfg.Model.Runtime, m.Cfg.Model.ToolProtocol = "openai", protocol
			m.Cfg.Tabs.TitleEvery = 0
			m.Chat.Running, m.Chat.Seq = true, 3
			m.Chat.Context = session.Continuation{Version: 1, Measured: true, Prompt: 55, Revision: 12, ModelKey: m.contextKey()}
			m.Chat.Context.Add("intent", "user", "RETAINED", false)
			if protocol == "text" {
				m.Chat.Context.Entries = nil // Fresh first run, before any terminal snapshot.
				m.Chat.Context.Version, m.Chat.Context.Prompt, m.Chat.Context.Measured = 0, 0, false
			}
			before := m.Chat.Context.Clone()
			messages := make(chan tea.Msg, 100)
			log := fanoutLog{logs: []agent.SessionLog{displayRawSession{t.TempDir()}, progLog{seq: 3, send: func(msg tea.Msg) { messages <- msg }}}}
			reg := tool.NewRegistry()
			reg.Register(tool.Definition{Name: "observe", Purpose: "Fixture observation.", Params: []tool.Param{{Name: "text", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
				return tool.Result{Text: args["text"].(string)}, nil
			}})
			client := llm.New(srv.URL+"/v1", "measurement-fixture").WithRuntime("openai").WithToolProtocol(protocol)
			var model agent.Model = client
			if protocol == "plain" {
				model = measurementPlainClient{client}
			}
			r := &agent.Runner{Model: model, ModelKey: m.contextKey(), Registry: reg, Session: log, SessionID: "measurement", Continuation: before, MaxSteps: 3, MaxTokens: 32768}
			done := make(chan runDoneMsg, 1)
			go func() {
				final, _, err := r.Run(ctx, "NEW PROMPT")
				done <- runDoneMsg{seq: 3, final: final, continuation: r.Continuation, in: r.UsedIn, out: r.UsedOut, unknown: r.UsageUnknown, outcome: r.Outcome, runErr: err}
			}()
			drain := func() {
				for {
					select {
					case msg := <-messages:
						next, _ := m.Update(msg)
						m = next.(Model)
					default:
						return
					}
				}
			}
			waitRequest := func(want int) {
				select {
				case n := <-entered:
					if n != want {
						t.Fatalf("request %d, want %d", n, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("fixture request not entered")
				}
				drain()
			}
			waitRequest(1)
			if m.Chat.Context.Measured {
				t.Error("new prompt retained old measured reading")
			}
			m.contextChat()
			if note := m.Alerts[len(m.Alerts)-1].Text; !strings.Contains(note, "prompt: unknown /") || !strings.Contains(note, "current run cost available after settlement") {
				t.Error("fresh pending context/cost unavailable", note)
			}
			close(release[0])
			waitRequest(2) // First reply is settled; second forward has not responded.
			if !m.Chat.Context.Measured || m.Chat.Context.Prompt != 100 || !strings.Contains(m.footer(), "100/") {
				t.Error("first measured request unavailable before runDone", m.Chat.Context.Prompt, m.Chat.Context.Measured)
			}
			m.contextChat()
			if !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "prompt: 100 /") {
				t.Error("/context did not expose live reading")
			}
			if !reflect.DeepEqual(before.Entries, m.Chat.Context.Entries) || before.Revision != m.Chat.Context.Revision || m.Chat.LastCostKnown {
				t.Error("live measurement replaced retained entries/revision/cost")
			}
			close(release[1])
			select {
			case result := <-done:
				drain()
				if result.runErr != nil || m.Chat.Context.Prompt != 200 || !m.Chat.Context.Measured || !m.Chat.Running {
					t.Error("second live reading", result.runErr, m.Chat.Context)
				}
				next, _ := m.Update(result)
				m = next.(Model)
				if m.Chat.LastIn != 300 || m.Chat.LastOut != 14 || m.Chat.Context.Prompt != 200 {
					t.Error("terminal ledger conflated with prompt")
				}
				m.newTab()
				m.switchTab(0)
				if m.Chat.Context.Prompt != 200 || !m.Chat.Context.Measured {
					t.Error("tab lost final reading")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("fixture run did not settle")
			}
		})
	}
}

func TestLivePromptAcceptedStartInvalidatesSynchronously(t *testing.T) {
	m := New(testCfg(), "measurement")
	m.ModelName, m.Cfg.Model.Name = "fixture", "fixture"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("failed store setup unexpectedly contacted model")
		w.WriteHeader(503)
	}))
	defer srv.Close()
	m.Server, m.Cfg.Model.Server = srv.URL+"/v1", srv.URL+"/v1"
	m.Cfg.Model.Runtime = "openai"
	// A regular file is not a Kuzu DB directory; no model request can start.
	m.StorePath = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(m.StorePath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Chat.Context = session.Continuation{Version: 1, Measured: true, Prompt: 200, ModelKey: m.contextKey()}
	m.Chat.Context.Add("intent", "user", "original", false)
	m.startRun("new visible prompt")
	if m.Chat.Context.Measured || m.Chat.Context.Prompt != 200 || m.Chat.Running {
		t.Fatal("accepted new prompt retained old measurement or started fixture")
	}
	m.Chat.Context.Measured, m.Chat.Running = true, true
	m.startRun("refused busy submission")
	if !m.Chat.Context.Measured {
		t.Fatal("busy refusal invalidated unchanged conversation")
	}
}

func TestLivePromptEventGuardsAndArchiveSeparation(t *testing.T) {
	m := New(testCfg(), "measurement")
	m.Chat.Running, m.Chat.Seq = true, 9
	m.Chat.Context = session.Continuation{Prompt: 100, Measured: true, ModelKey: m.contextKey()}
	raw, _ := json.Marshal(session.Continuation{Prompt: 200, Measured: true, ModelKey: m.contextKey()})
	for _, msg := range []eventMsg{{seq: 8, kind: "context", text: string(raw)}, {seq: 9, kind: "context", text: "not JSON"}, {seq: 9, kind: "reasoning", text: `{"Partial":true,"Usage":{"In":999,"Reported":true}}`}} {
		next, _ := m.Update(msg)
		m = next.(Model)
		if m.Chat.Context.Prompt != 100 || !m.Chat.Context.Measured {
			t.Fatal("stale/malformed/partial archive became a live reading")
		}
	}
	m.Chat.Running = false
	next, _ := m.Update(eventMsg{seq: 9, kind: "context", text: string(raw)})
	m = next.(Model)
	if m.Chat.Context.Prompt != 100 {
		t.Fatal("late settled event changed reading")
	}
	m.Chat.Running = true
	foreign, _ := json.Marshal(session.Continuation{Prompt: 999, Measured: true, ModelKey: "other-runtime"})
	next, _ = m.Update(eventMsg{seq: 9, kind: "context", text: string(foreign)})
	m = next.(Model)
	if !strings.Contains(m.footer(), "prompt unknown/") {
		t.Fatal("foreign measurement claimed current denominator")
	}
}
