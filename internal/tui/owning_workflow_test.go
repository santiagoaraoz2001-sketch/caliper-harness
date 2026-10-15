package tui

import (
	"context"
	"encoding/json"
	tea "github.com/charmbracelet/bubbletea"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Actual workflow command, Model.Update and HTTP transport. Native judge/model
// responses are deterministic component fixtures, not inference qualification.
func TestOwningWorkflowCommandDialog(t *testing.T) {
	for _, answer := range []string{"y", "n", "esc", "stop", "quit"} {
		t.Run(answer, func(t *testing.T) {
			cfg := testCfg()
			cfg.Context.Window = 32768
			cfg.Templates.Path = "../../templates"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Model.Name = "component"
			cfg.Model.Runtime = "openai"
			cfg.Model.ToolProtocol = "text"
			cfg.Workflows.VerifyFollowup = true
			cfg.Decision.Model = "component"
			cfg.Decision.Device = "cpu"
			cfg.Decision.Runtime = "laya"
			sel := decision.SelectionFor(cfg.Decision)
			var forwards atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/health":
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: sel}, "backends": []string{"cpu"}})
				case "/judge":
					var q decision.Request
					if err := json.NewDecoder(r.Body).Decode(&q); err != nil || len(q.Options) == 0 {
						t.Errorf("invalid judge request: %v", err)
						http.Error(w, "invalid request", 400)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": q.Options[0], "confidence": .1, "model": "component", "device": "cpu"})
				case "/chat/completions":
					forwards.Add(1)
					var body struct {
						Messages []llm.Message
						Stream   bool
					}
					json.NewDecoder(r.Body).Decode(&body)
					text := "worker completed"
					if strings.HasPrefix(body.Messages[len(body.Messages)-1].Content, "Review ") {
						text = "PASS: actual true check"
					}
					if body.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						chunk, _ := json.Marshal(map[string]any{"model": "component", "choices": []any{map[string]any{"delta": map[string]string{"content": text}, "finish_reason": "stop"}}})
						io.WriteString(w, "data: "+string(chunk)+"\n\ndata: [DONE]\n\n")
					} else {
						json.NewEncoder(w).Encode(map[string]any{"model": "component", "choices": []any{map[string]any{"message": llm.Message{Role: "assistant", Content: text}, "finish_reason": "stop"}}})
					}
				default:
					t.Errorf("unexpected fixture route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			cfg.Decision.Server = srv.URL
			cfg.Model.Server = srv.URL
			path := filepath.Join(t.TempDir(), "db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			p, err := plan.CreatePlanContext(plan.WithProject(context.Background(), root), s, "p", "g")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
				t.Fatal(err)
			}
			a, _ := plan.CreateApproach(s, p.ID, "a")
			x, _ := plan.CreateTask(s, a.ID, "task", "complete worker")
			plan.SetTaskBlock(s, x.ID, "current", "target", "sketch", "exit code 0", "tests", strings.Repeat("Review actual evidence ", 35)+"FINAL_VERIFY", "true", "printf UI_FIXTURE_ACCEPTANCE")
			if _, err := plan.Ready(context.Background(), s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
				t.Fatal(err)
			}
			s.Close()
			m := New(cfg, "test")
			m.StorePath = path
			m.Server = srv.URL
			m.ModelName = "component"
			m.Chat.Stream.WriteString("parent bytes")
			m.Chat.History = []llm.Message{{Role: "user", Content: "parent intent"}}
			cmd := m.wfLaunch(WfRow{Approach: a.ID, State: plan.ApproachReady}, false)
			if cmd == nil {
				t.Fatal("no workflow command")
			}
			shown := make(chan showDialogMsg, 1)
			done := make(chan wfDoneMsg, 1)
			loaded := make(chan wfLoadedMsg, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			prog := tea.NewProgram(fidelityProgram{m}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithContext(ctx), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
				switch v := msg.(type) {
				case showDialogMsg:
					shown <- v
				case wfDoneMsg:
					done <- v
				case wfLoadedMsg:
					loaded <- v
				}
				return msg
			}))
			m.Shared.Prog = prog
			finish := make(chan tea.Model, 1)
			go func() {
				v, e := prog.Run()
				if e != nil && ctx.Err() == nil {
					t.Errorf("program: %v", e)
				}
				finish <- v
			}()
			defer prog.Quit()
			go func() { prog.Send(cmd()) }()
			select {
			case d := <-shown:
				if d.workflow != a.ID || !strings.Contains(d.prompt, "unsure") {
					t.Error("wrong owner/prompt", d)
				}
				for _, size := range [][2]int{{80, 24}, {120, 40}} {
					view := inspectionUpdate(New(cfg, "inspection"), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					view.Wf.Busy, view.Wf.ctx, view.Chat.Seq = d.workflow, d.ctx, d.seq
					view.Input.SetValue("approval draft unchanged")
					view = inspectionUpdate(view, d)
					all := inspectionFrame(t, view)
					for page := 0; page < 10; page++ {
						view = inspectionUpdate(view, tea.KeyMsg{Type: tea.KeyPgDown})
						all += "\n" + inspectionFrame(t, view)
					}
					for _, wanted := range []string{"Plan goal: g", "complete worker", "FINAL_VERIFY", "check_command: true"} {
						if !strings.Contains(all, wanted) {
							t.Errorf("approval %v missing %q", size, wanted)
						}
					}
					view = inspectionUpdate(view, tea.KeyMsg{Type: tea.KeyEnd})
					if !strings.Contains(inspectionFrame(t, view), "check_command: true") {
						t.Error("final check inaccessible")
					}
					view = inspectionUpdate(view, tea.KeyMsg{Type: tea.KeyHome})
					if !strings.Contains(inspectionFrame(t, view), "Plan goal: g") || view.Input.Value() != "approval draft unchanged" || view.Wf.Busy != a.ID {
						t.Error("approval paging changed draft/origin")
					}
					select {
					case answer := <-d.answer:
						t.Fatalf("paging answered %q", answer)
					default:
					}
				}
			case <-ctx.Done():
				t.Fatal("uncertainty dialog missing")
			}
			if answer == "stop" {
				prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/stop")})
				prog.Send(tea.KeyMsg{Type: tea.KeyEnter})
			} else if answer == "quit" {
				prog.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
			} else if answer == "esc" {
				prog.Send(tea.KeyMsg{Type: tea.KeyEsc})
			} else {
				prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(answer)})
			}
			var settled wfDoneMsg
			select {
			case settled = <-done:
			case <-ctx.Done():
				t.Fatal("workflow did not settle")
			}
			// onWfDone starts a refresh; join its Store.Close before artificial quit.
			if answer != "quit" {
				select {
				case msg := <-loaded:
					if msg.err != nil {
						t.Fatal(msg.err)
					}
				case <-ctx.Done():
					t.Fatal("post-settlement refresh did not finish")
				}
			}
			prog.Quit()
			var final Model
			select {
			case v := <-finish:
				final = v.(Model)
			case <-ctx.Done():
				t.Fatal("UI did not settle")
			}
			if final.Wf.Busy != "" || final.Chat.Dialog != nil || final.Chat.Stream.String() != "parent bytes" || len(final.Chat.History) != 1 {
				t.Fatalf("parent/owner overwritten %+v", final.Chat)
			}
			s, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			task, _ := plan.GetTask(s, x.ID)
			if answer == "y" {
				if settled.err != nil || !task.Qualified() || forwards.Load() != 2 {
					t.Fatalf("accept: %+v %+v forwards%d", settled, task, forwards.Load())
				}
			} else if settled.err == nil || task.Attempts != 0 || forwards.Load() != 0 {
				t.Fatalf("decline executed %+v %+v forwards%d", settled, task, forwards.Load())
			}
		})
	}
}
func TestOwningWorkflowDialogOriginAndGuards(t *testing.T) {
	m := New(testCfg(), "test")
	m.newTab()
	m.Active = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Wf.Busy = "owned"
	m.Wf.ctx = ctx
	m.Wf.cancel = cancel
	m.Chat.Stream.WriteString("parent")
	update := func(msg tea.Msg) { v, _ := m.Update(msg); m = v.(Model) }
	ch := make(chan string, 1)
	update(showDialogMsg{ctx: ctx, seq: m.Chat.Seq, workflow: "owned", kind: "auth", prompt: "confirm", answer: ch})
	for _, k := range []string{"ctrl+n", "ctrl+t", "ctrl+w"} {
		before := len(m.Tabs)
		m = key(m, k)
		if m.Active != 0 || len(m.Tabs) != before || m.Chat.Dialog == nil {
			t.Fatal("dialog transplanted", k)
		}
	}
	m.stopChat()
	if <-ch != "n" || ctx.Err() == nil || m.Wf.Busy != "owned" || m.Chat.Dialog != nil {
		t.Fatal("stop failed to decline/retain busy")
	}
	next, stop := context.WithCancel(context.Background())
	defer stop()
	m.Wf.ctx = next
	m.Wf.cancel = stop
	stale := make(chan string, 1)
	update(showDialogMsg{ctx: ctx, workflow: "owned", kind: "auth", answer: stale})
	if <-stale != "n" || m.Chat.Dialog != nil {
		t.Fatal("cancelled callback revived")
	}
	update(wfDoneMsg{ctx: ctx, approach: "owned"})
	if m.Wf.Busy != "owned" {
		t.Fatal("stale settlement cleared owner")
	}
	m.Input.SetValue("do work")
	m.startRun("do work")
	if m.Chat.Running || m.Input.Value() != "do work" {
		t.Fatal("workflow busy permitted parent run")
	}
	m.Wf.Busy = ""
	m.Chat.Running = true
	if cmd := m.wfLaunch(WfRow{Approach: "other"}, false); cmd != nil || m.Wf.Busy != "" {
		t.Fatal("running parent admitted workflow")
	}
}
