package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tui/thirdparty/textarea"
)

func fidelityKey(m Model, k tea.KeyType) Model {
	v, _ := m.Update(tea.KeyMsg{Type: k})
	return v.(Model)
}
func fidelityPaste(m Model, s string) Model {
	v, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true})
	return v.(Model)
}
func fidelityHeight(m Model) int {
	if h, ok := any(m.Input).(interface{ Height() int }); ok {
		return h.Height()
	}
	return -1
}
func TestInputFidelityPasteEditingDraftHistory(t *testing.T) {
	raw := " \tα\r\n\r\n    β\rTAIL \t\n"
	want := " \tα\n\n    β\nTAIL \t\n"
	m := fidelityPaste(New(testCfg(), "test"), raw)
	if m.Input.Value() != want {
		t.Fatalf("paste%q", m.Input.Value())
	}
	m = fidelityKey(m, tea.KeyCtrlJ)
	want += "\n"
	if m.Input.Value() != want {
		t.Fatalf("newline%q", m.Input.Value())
	}
	m = fidelityKey(m, tea.KeyShiftRight)
	m = fidelityPaste(m, "other")
	m = fidelityKey(m, tea.KeyShiftLeft)
	if m.Input.Value() != want {
		t.Fatalf("draft%q", m.Input.Value())
	}
	m.ModelName = ""
	m = fidelityKey(m, tea.KeyEnter)
	if len(m.Hist) != 1 || m.Hist[0] != want {
		t.Fatalf("history%q", m.Hist)
	}
	m = fidelityPaste(m, "live\n\tdraft")
	m = fidelityKey(m, tea.KeyShiftUp)
	if m.Input.Value() != want {
		t.Fatalf("recall%q", m.Input.Value())
	}
	m = fidelityKey(m, tea.KeyShiftDown)
	if m.Input.Value() != "live\n\tdraft" {
		t.Fatalf("live draft%q", m.Input.Value())
	}
	m.Input.SetValue("a\tb\n123")
	m.Input.SetCursor(2)
	m = fidelityKey(m, tea.KeyUp)
	m = fidelityKey(m, tea.KeyBackspace)
	if m.Input.Value() != "ab\n123" {
		t.Fatalf("caret deleted wrong rune%q", m.Input.Value())
	}
	m = fidelityPaste(m, "\t")
	if m.Input.Value() != "a\tb\n123" {
		t.Fatalf("tab restore%q", m.Input.Value())
	}
	if !strings.Contains(ansi.Strip(m.View()), "a→b") {
		t.Fatal("tab display missing")
	}
}
func TestInputFidelityGeometryAndCommands(t *testing.T) {
	m := New(testCfg(), "test")
	m = fidelityPaste(m, strings.Repeat("line\n", 8))
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 40, Height: 12}} {
		v, _ := m.Update(size)
		m = v.(Model)
		h := fidelityHeight(m)
		if h < 1 || h > 5 || strings.Count(m.View(), "\n")+1 > size.Height {
			t.Fatalf("size%+v height%d frame%d", size, h, strings.Count(m.View(), "\n")+1)
		}
		if m.Input.Value() != strings.Repeat("line\n", 8) {
			t.Fatal("resize changed data")
		}
	}
	if !strings.Contains(ansi.Strip(m.View()), "Ctrl+J newline") || !strings.Contains(keyHelp(), "→ marks tabs") {
		t.Fatal("input hints absent")
	}
	m.Input.SetValue("/help\nliteral")
	m.ModelName = ""
	m = fidelityKey(m, tea.KeyEnter)
	if m.HelpOpen {
		t.Fatal("multiline paste executed slash")
	}
	m.Input.SetValue("/quit\n")
	m = fidelityKey(m, tea.KeyEnter)
	if m.quitPending {
		t.Fatal("trailing newline paste executed command")
	}
	m.Input.SetValue(" /help ")
	m = fidelityKey(m, tea.KeyEnter)
	if !m.HelpOpen {
		t.Fatal("single-line slash tolerance lost")
	}
}
func TestInputFidelityAllOverflowPathsAtomic(t *testing.T) {
	tooMany := strings.Repeat("x\n", textarea.LineLimit)
	for _, path := range []string{"paste", "setvalue", "typed"} {
		t.Run(path, func(t *testing.T) {
			m := New(testCfg(), "test")
			m.Input.SetValue("KEEP\t ")
			if path == "typed" {
				m.Input.SetValue(strings.Repeat("\n", textarea.LineLimit-1))
			}
			before := m.Input.Value()
			switch path {
			case "paste":
				m = fidelityPaste(m, tooMany)
			case "setvalue":
				m.Input.SetValue(tooMany)
				v, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				m = v.(Model)
			case "typed":
				m = fidelityKey(m, tea.KeyCtrlJ)
			}
			if m.Input.Value() != before {
				t.Fatalf("%s changed prior draft", path)
			}
			if len(m.Alerts) == 0 || !strings.Contains(m.Alerts[len(m.Alerts)-1].Text, "10000") {
				t.Fatalf("%s no visible refusal%+v", path, m.Alerts)
			}
		})
	}
}
func TestInputFidelityAskRawAndCommentOverflow(t *testing.T) {
	raw := "  BEFORE\r\n\tINDENTED\r\n\r\nAFTER\r"
	want := "  BEFORE\n\tINDENTED\n\nAFTER\n\n"
	for _, options := range []bool{false, true} {
		t.Run(fmt.Sprint(options), func(t *testing.T) {
			m := New(testCfg(), "test")
			done := make(chan []ask.Answer, 1)
			q := ask.Question{Text: "code"}
			if options {
				q.Options = []string{"yes", "no"}
			}
			m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{q}, AskDone: done, Commenting: options}
			m = fidelityPaste(m, raw)
			m = fidelityKey(m, tea.KeyCtrlJ)
			m = fidelityKey(m, tea.KeyEnter)
			select {
			case a := <-done:
				got := a[0].Picked
				if options {
					got = a[0].Comment
				}
				if got != want {
					t.Fatalf("raw answer%q", got)
				}
			case <-time.After(time.Second):
				t.Fatal("answer not delivered")
			}
		})
	}
	m := New(testCfg(), "test")
	m.Chat.Dialog = &Dialog{Kind: "ask", Questions: []ask.Question{{Text: "code", Options: []string{"a", "b"}}}, Commenting: true, Comment: "KEEP"}
	m = fidelityPaste(m, strings.Repeat("x\n", textarea.LineLimit))
	if m.Chat.Dialog.Comment != "KEEP" || len(m.Alerts) == 0 {
		t.Fatal("comment overflow was not atomic")
	}
}
func TestInputFidelitySettingsArmedPath(t *testing.T) {
	m := New(testCfg(), "test")
	m.navigate(ViewSettings, "")
	for _, field := range settingFields() {
		if field.Section+"/"+field.Key == "params/temperature" {
			m.armEdit(field)
			break
		}
	}
	m.Input.SetValue("0.25")
	m = fidelityKey(m, tea.KeyEnter)
	if m.Cfg.Params.Temperature != .25 || m.Settings.Editing != nil {
		t.Fatalf("armed edit%+v", m.Settings)
	}
	for _, field := range settingFields() {
		if field.Section+"/"+field.Key == "params/temperature" {
			m.armEdit(field)
			break
		}
	}
	m = fidelityKey(m, tea.KeyEsc)
	if m.Settings.Editing != nil || m.Input.Value() != "" {
		t.Fatal("esc edit failed")
	}
}

// Only Init is suppressed in this component driver to avoid status/watch/service probes.
// Model.Update, asynchronous Runner, real HTTP transport, Kuzu and session remain actual.
type fidelityProgram struct{ Model }

func (fidelityProgram) Init() tea.Cmd { return nil }
func TestInputFidelityPromptWireAndNativeSession(t *testing.T) {
	for _, stream := range []bool{true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			dir := t.TempDir()
			if parent := os.Getenv("R41_EVIDENCE_ROOT"); parent != "" {
				var err error
				dir, err = os.MkdirTemp(parent, "native-")
				if err != nil {
					t.Fatal(err)
				}
			}
			requests := make(chan []byte, 2)
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				requests <- data
				var request struct{ Stream bool }
				if err := json.Unmarshal(data, &request); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				name := "request.json"
				if !request.Stream {
					name = "request-plain.json"
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if request.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"model\":\"explicit-fixture\",\"choices\":[{\"delta\":{\"content\":\"fixture reply\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":80,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"model":"explicit-fixture","choices":[{"message":{"content":"fixture reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":3}}`)
				}
			}))
			defer fixture.Close()
			cfg := config.Defaults()
			cfg.Model.Server = fixture.URL + "/v1"
			cfg.Model.Name = "explicit-fixture"
			cfg.Model.Runtime = "openai"
			cfg.Model.ToolProtocol = "text" // composer fidelity uses explicit compatibility; native schema admission is tested separately
			cfg.Context.Window = 32768
			cfg.Store.Path = filepath.Join(dir, "kuzu")
			cfg.Sessions.Path = filepath.Join(dir, "sessions")
			cfg.Behavior.Path = filepath.Join(dir, "no-behavior")
			cfg.Templates.Path = filepath.Join(dir, "no-templates")
			// This isolated source now supplies the shared catalog rather than
			// relying on a silently ignored absent legacy template directory.
			if err := os.Mkdir(cfg.Templates.Path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.Templates.Path, "prompts.yaml"), []byte(prompts.Builtin().YAML()), 0600); err != nil {
				t.Fatal(err)
			}
			cfg.Watch.Enabled = false
			cfg.Tabs.TitleEvery = 0
			cfg.Agent.MaxSteps = 1 // One native request; plain transport is a separate fidelity control.
			cfg.Agent.LoopRepeat = 0
			cfg.Agent.LoopStall = 0
			cfg.Auth.Mode = config.AuthYOLO
			// Composition makes one actual request with no check; missing-check stop remains truthful.
			cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
			selection := decision.SelectionFor(cfg.Decision)
			judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: selection}, "backends": []string{"cpu"}})
				case "/judge":
					var request decision.Request
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": request.Options[0], "confidence": 1, "model": "component", "device": "cpu"})
				default:
					t.Errorf("unbound decision route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer judge.Close()
			cfg.Decision.Server = judge.URL
			m := New(cfg, "r41-component")
			settled := make(chan struct{}, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p := tea.NewProgram(fidelityProgram{m}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithContext(ctx), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
				if _, ok := msg.(runDoneMsg); ok {
					settled <- struct{}{}
				}
				return msg
			}))
			m.Shared.Prog = p
			type result struct {
				m tea.Model
				e error
			}
			finished := make(chan result, 1)
			go func() { v, e := p.Run(); finished <- result{v, e} }()
			defer p.Quit()
			raw := "  def hello():\r\n\treturn 'café 😀'\r\n\r\n  "
			want := "  def hello():\n\treturn 'café 😀'\n\n  "
			p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(raw), Paste: true})
			p.Send(tea.KeyMsg{Type: tea.KeyEnter})
			select {
			case <-settled:
			case <-ctx.Done():
				t.Fatal("actual run did not settle")
			}
			p.Quit()
			var final Model
			select {
			case got := <-finished:
				if got.e != nil {
					t.Fatal(got.e)
				}
				final = got.m.(Model)
			case <-ctx.Done():
				t.Fatal("program did not settle")
			}
			if final.Chat.Running || final.Chat.LastPrompt != want || final.Chat.LastFull != want || len(final.Hist) != 1 || final.Hist[0] != want {
				t.Fatalf("submitted data mismatch last%q full%q hist%q running%v", final.Chat.LastPrompt, final.Chat.LastFull, final.Hist, final.Chat.Running)
			}
			var wire []byte
			select {
			case wire = <-requests:
			default:
				t.Fatalf("no actual HTTP request; settled transcript: %+v", final.Chat.Lines)
			}
			var req struct {
				Messages []struct{ Role, Content string }
			}
			if err := json.Unmarshal(wire, &req); err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, message := range req.Messages {
				if message.Role == "user" && message.Content == want {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("wire user content%q", req.Messages)
			}
			ids, err := session.List(cfg.Sessions.Path)
			if err != nil || len(ids) != 1 {
				t.Fatalf("sessions%v %v", ids, err)
			}
			events, err := session.Read(cfg.Sessions.Path, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			havePrompt, haveStopped := false, false
			for _, e := range events {
				if e.Kind == "prompt" {
					havePrompt = e.Text == want
				}
				if e.Kind == "stopped" && strings.Contains(e.Text, "current declared check missing, denied, incomplete or stale") {
					haveStopped = true
				}
			}
			if !havePrompt || !haveStopped {
				t.Fatalf("session raw events%+v", events)
			}
			if err := os.WriteFile(filepath.Join(dir, "request.json"), wire, 0600); err != nil {
				t.Fatal(err)
			}
			plain, _, _, err := llm.New(cfg.Model.Server, cfg.Model.Name).Chat(ctx, []llm.Message{{Role: "user", Content: final.Chat.LastFull}})
			if err != nil || plain != "fixture reply" {
				t.Fatalf("plain transport control %q %v", plain, err)
			}
			plainWire := <-requests
			if err := json.Unmarshal(plainWire, &req); err != nil || len(req.Messages) != 1 || req.Messages[0].Content != want {
				t.Fatalf("plain wire mismatch %s %v", plainWire, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "request-plain.json"), plainWire, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("component HTTP stream%v actual settled session%s request%s canonical%q", stream, filepath.Join(cfg.Sessions.Path, ids[0]+".jsonl"), filepath.Join(dir, "request.json"), want)
		})
	}
}
