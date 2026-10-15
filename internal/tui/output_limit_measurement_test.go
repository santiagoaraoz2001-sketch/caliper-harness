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

	tea "github.com/charmbracelet/bubbletea"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Exercise the streamed length terminal through Runner, its existing event sink,
// and the real TUI update/render path while the literal continuation is pending.
func TestOutputLimitPromptMeasurementBeforeContinuationSettles(t *testing.T) {
	for _, reported := range []bool{true, false} {
		t.Run(fmt.Sprintf("reported=%t", reported), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan int, 3)
			release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			var requests []map[string]json.RawMessage
			prefix := map[string]any{"tokens": []int{11}, "raw_chunks": []string{"unfinished thought "}, "initial_state": "reasoning", "prompt_sha256": strings.Repeat("a", 64), "tool_index": 0}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				requests = append(requests, body)
				n := len(requests)
				if n > len(release) {
					t.Error("unexpected extra continuation")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				entered <- n
				select {
				case <-release[n-1]:
				case <-req.Context().Done():
					return
				}
				delta, reason := map[string]any{"content": "done", "reasoning": "now done"}, "stop"
				if n == 1 {
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "observed-call", "type": "function", "function": map[string]string{"name": "observe", "arguments": `{}`}}}}
					reason = "tool_calls"
				} else if n == 2 {
					delta, reason = map[string]any{"reasoning": "unfinished thought "}, "length"
				}
				row := map[string]any{"model": "measurement-fixture", "choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}}
				if n == 1 {
					row["usage"] = map[string]int{"prompt_tokens": 29173, "completion_tokens": 7}
				} else if n == 2 {
					row["response_prefix"] = prefix
					if reported {
						row["usage"] = map[string]int{"prompt_tokens": 47684, "completion_tokens": 32768}
					}
				} else {
					row["usage"] = map[string]int{"prompt_tokens": 80000, "completion_tokens": 9}
				}
				raw, _ := json.Marshal(row)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			}))
			defer func() { cancel(); srv.Close() }()
			m := New(testCfg(), "length-measurement")
			m.Server, m.Cfg.Model.Server = srv.URL+"/v1", srv.URL+"/v1"
			m.ModelName, m.Cfg.Model.Name = "measurement-fixture", "measurement-fixture"
			m.Cfg.Model.Runtime, m.Cfg.Model.ToolProtocol = "mlx", "structured"
			m.Cfg.Tabs.TitleEvery = 0
			m.St.CtxWindow = 262144
			m.Chat.Running, m.Chat.Seq = true, 3
			m.Chat.Context = session.Continuation{Version: 1, Revision: 12, ModelKey: m.contextKey()}
			m.Chat.Context.Add("intent", "user", "RETAINED", false)
			before := m.Chat.Context.Clone()
			messages := make(chan tea.Msg, 100)
			log := fanoutLog{logs: []agent.SessionLog{displayRawSession{t.TempDir()}, progLog{seq: 3, send: func(msg tea.Msg) { messages <- msg }}}}
			reg, effects := tool.NewRegistry(), 0
			reg.Register(tool.Definition{Name: "observe", Purpose: "Read an already supplied fixture observation.", Execute: func(context.Context, tool.Args) (tool.Result, error) {
				effects++
				return tool.Result{Text: "observed fixture"}, nil
			}})
			r := &agent.Runner{Model: llm.New(srv.URL+"/v1", "measurement-fixture").WithRuntime("mlx"), ModelKey: m.contextKey(), Registry: reg, Session: log, SessionID: "length-measurement", Continuation: before, MaxTokens: 262144}
			done := make(chan runDoneMsg, 1)
			go func() {
				final, _, err := r.Run(ctx, "retain the original objective")
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
					t.Fatal("stream request not entered")
				}
				drain()
			}
			waitRequest(1)
			close(release[0])
			waitRequest(2)
			if !m.Chat.Context.Measured || m.Chat.Context.Prompt != 29173 {
				t.Fatalf("first response measurement missing: %+v", m.Chat.Context)
			}
			close(release[1])
			waitRequest(3) // The output-limited response is processed; continuation has no reply yet.
			wantReading, wantFooter := "unknown", "prompt unknown/"
			if reported {
				wantReading, wantFooter = kfmt(47684), kfmt(47684)+"/"
				if !m.Chat.Context.Measured || m.Chat.Context.Prompt != 47684 {
					t.Errorf("output-limited reported prompt stayed stale: %+v", m.Chat.Context)
				}
			} else if m.Chat.Context.Measured {
				t.Errorf("output-limited missing usage retained prior measured prompt: %+v", m.Chat.Context)
			}
			m.contextChat()
			if !strings.Contains(m.footer(), wantFooter) || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "prompt: "+wantReading+" /") {
				t.Errorf("length measurement absent from footer or /context: %s; %s", m.footer(), m.Alerts[len(m.Alerts)-1].Text)
			}
			if !reflect.DeepEqual(before.Entries, m.Chat.Context.Entries) || before.Revision != m.Chat.Context.Revision || m.Chat.LastCostKnown || m.Chat.LastIn != 0 || m.Chat.LastOut != 0 {
				t.Error("prompt event replaced retained context or cumulative run totals")
			}
			if string(requests[2]["messages"]) != string(requests[1]["messages"]) {
				t.Error("literal continuation changed base messages or injected a Continue message")
			}
			var sentPrefix map[string]any
			if err := json.Unmarshal(requests[2]["response_prefix"], &sentPrefix); err != nil {
				t.Fatal("literal continuation prefix missing", err)
			}
			wantPrefix, _ := json.Marshal(prefix)
			sent, _ := json.Marshal(sentPrefix)
			if string(sent) != string(wantPrefix) {
				t.Errorf("literal prefix changed: %s", sent)
			}
			close(release[2])
			select {
			case result := <-done:
				drain()
				if result.runErr != nil || result.outcome.Status != outcome.Completed || result.final != "done" || effects != 1 || !m.Chat.Context.Measured || m.Chat.Context.Prompt != 80000 {
					t.Errorf("continued response failed: %+v; effects=%d; prompt=%+v", result, effects, m.Chat.Context)
				}
				next, _ := m.Update(result)
				m = next.(Model)
				wantIn, wantOut := 29173+80000, 7+9
				if reported {
					wantIn, wantOut = wantIn+47684, wantOut+32768
				}
				if m.Chat.LastIn != wantIn || m.Chat.LastOut != wantOut || m.Chat.LastUnknown == reported || m.Chat.Context.Prompt != 80000 {
					t.Errorf("prompt reading mixed with cumulative totals: in/out=%d/%d unknown=%t prompt=%d", m.Chat.LastIn, m.Chat.LastOut, m.Chat.LastUnknown, m.Chat.Context.Prompt)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("continuation did not settle")
			}
		})
	}
}
