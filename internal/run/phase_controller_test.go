package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

// Model and decision are scripted; owner, files, Bash, checks and reviews are real.
func TestMainPhaseCheckRemediation(t *testing.T) {
	for _, mode := range []string{"check", "review", "unverified", "budget", "cancel", "next"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Templates.Path = assignedTemplates()
			cfg.Coding.CheckCommand = `test "$(cat marker)" = good`
			bindDecisionFixture(t, &cfg, nil)
			task := assignedFixtureTask(t, s, cfg.Coding.CheckCommand)
			calls := []codingCall{
				{"task_start", map[string]any{"task": task.ID}},
				{"bash", map[string]any{"cmd": "printf bad > marker"}},
				{"check", map[string]any{"timeout": 2}},
				{}, // Normal completion requests validation, never asserts qualification.
				{"graphql", map[string]any{"query": fmt.Sprintf(`mutation { transitionTask(id:%q,to:"queued") { id state } }`, task.ID)}},
				{"task_start", map[string]any{"task": task.ID}},
				{"bash", map[string]any{"cmd": "printf good > marker"}},
				{"check", map[string]any{"timeout": 2}},
				{},
				{}, // Consume qualified progress before answering the user.
			}
			var nextTask *plan.Task
			if mode == "next" {
				nextTask = assignedFixtureTask(t, s, cfg.Coding.CheckCommand)
				calls = append(calls[:len(calls)-1], codingCall{"workflow_focus", map[string]any{"ref": nextTask.ID}}, codingCall{"task_start", map[string]any{"task": nextTask.ID}}, codingCall{"bash", map[string]any{"cmd": "printf good > marker"}}, codingCall{"check", map[string]any{"timeout": 2}}, codingCall{}, codingCall{})
			}
			if mode == "review" || mode == "unverified" {
				calls[1].args["cmd"] = "printf good > marker"
			}
			mainCalls, reviewCalls := 0, 0
			var workspace string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Tools    []struct{ Function struct{ Name string } }
					Messages []struct{ Role, Content string }
				}
				if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				review, text := false, ""
				for _, m := range body.Messages {
					review = review || strings.HasPrefix(m.Content, "Review ")
					text += m.Content + "\n"
				}
				var call codingCall
				if review {
					if reviewCalls%2 == 0 {
						call = codingCall{"check", map[string]any{"timeout": 2}}
					}
					reviewCalls++
				} else {
					if mainCalls >= len(calls) {
						t.Errorf("unexpected main call %d", mainCalls)
						return
					}
					coding := false
					for _, def := range body.Tools {
						coding = coding || def.Function.Name == "bash"
					}
					guides := 0
					for _, m := range body.Messages {
						if m.Role == "system" {
							guides += strings.Count(m.Content, "Implement the requested behavior")
						}
					}
					wantGuides := 0
					currentID := task.ID
					if mode == "next" && mainCalls >= 11 {
						currentID = nextTask.ID
					}
					current, err := plan.GetTask(s, currentID)
					if err != nil {
						t.Error(err)
					}
					if current != nil && current.State == plan.TaskRunning && current.Attempts > 0 {
						wantGuides = 1
					}
					if guides != wantGuides || !strings.Contains(body.Messages[0].Content, "Workspace: "+workspace) || !strings.Contains(body.Messages[0].Content, "Configured check: "+cfg.Coding.CheckCommand) {
						t.Errorf("request%d phase coding=%t guides=%d want%d; host environment missing or stale", mainCalls, coding, guides, wantGuides)
					}
					if mainCalls == 0 || mainCalls == 4 || mainCalls == 5 {
						names := map[string]bool{}
						for _, def := range body.Tools {
							names[def.Function.Name] = true
							if mainCalls != 0 && (def.Function.Name == "bash" || def.Function.Name == "edit" || def.Function.Name == "write" || def.Function.Name == "check") {
								t.Errorf("settled phase advertises effect %s", def.Function.Name)
							}
						}
						for _, name := range []string{"read", "find_file", "find_dir", "help", "grep", "graphql", "task_start", "ask"} {
							if !names[name] {
								t.Errorf("missing legal planning tool %s", name)
							}
						}
						if mainCalls == 0 {
							for _, name := range []string{"bash", "edit", "write", "check"} {
								if names[name] {
									t.Errorf("draft phase exposes effect %s", name)
								}
							}
						}
					}
					if mainCalls == 4 {
						actual, e := plan.GetTask(s, task.ID)
						if e != nil || actual.State != plan.TaskFailed || actual.Qualified() || actual.Attempts != 1 {
							t.Errorf("remediation lost actual failure: %+v %v", actual, e)
						}
						if !strings.Contains(text, task.ID) || !(strings.Contains(text, "exited1") || mode == "review" && strings.Contains(text, "FAIL observed review rejection")) {
							t.Error("current rejection missing from remediation input")
						}
					}
					if mainCalls == 3 && mode == "cancel" {
						cancel()
					}
					lastTask := task
					if mode == "next" && mainCalls > 9 {
						lastTask = nextTask
					}
					if mode == "next" && mainCalls == 9 && !strings.Contains(text, "VALIDATED task="+task.ID) {
						t.Error("first qualification never reached next-task selection")
					}
					if mode == "next" && mainCalls == 11 {
						base := body.Messages[0].Content
						if !strings.Contains(base, "EXECUTE claimed task "+nextTask.ID+" attempt1") || strings.Contains(base, "EXECUTE claimed task "+task.ID) {
							t.Error("new task inherited stale role identity")
						}
					}
					if mainCalls == len(calls)-1 && !strings.Contains(text, "VALIDATED task="+lastTask.ID) {
						t.Error("qualified settlement never reached consuming request")
					}
					call = calls[mainCalls]
					mainCalls++
				}
				delta, finish := map[string]any{"content": "PASS complete"}, "stop"
				if review && reviewCalls == 2 {
					if mode == "review" {
						delta["content"] = "FAIL observed review rejection"
					}
					if mode == "unverified" {
						delta["content"] = "UNVERIFIED unavailable verification"
					}
				}
				if call.name != "" {
					raw, _ := json.Marshal(call.args)
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d-%d", mainCalls, reviewCalls), "type": "function", "function": map[string]string{"name": call.name, "arguments": string(raw)}}}}
					finish = "tool_calls"
				}
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer srv.Close()
			cfg.Model.Server = srv.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}, Cfg: cfg, Store: s, Session: sink, SessionID: "phases", Server: srv.URL, Model: "fixture", Window: 262144, MaxSteps: len(calls)}, "repair the marker and validate")
			if e != nil {
				t.Fatal(e)
			}
			workspace = r.Workspace
			// This scripted fixture checks role handoffs; compaction has separate fixtures.
			if mode == "budget" {
				r.MaxSteps = 4
			}
			_, _, e = r.Run(ctx, "repair the marker and validate")
			srv.Close() // Join cancelled handlers before reading their request counters.
			actual, readErr := plan.GetTask(s, task.ID)
			if mode == "budget" || mode == "cancel" {
				want, wantState := outcome.Failed, plan.TaskFailed
				if mode == "cancel" {
					want, wantState = outcome.Cancelled, plan.TaskCancelled
				}
				if r.Outcome.Status != want || actual.State != wantState || actual.Qualified() || actual.Attempts != 1 || mainCalls != 4 || reviewCalls != 0 {
					t.Fatalf("stop/budget resumed: %+v %+v %d/%d", r.Outcome, actual, mainCalls, reviewCalls)
				}
				return
			}
			if mode == "unverified" {
				if e != nil || readErr != nil || r.Outcome.Status != outcome.Stopped || actual.Qualified() || mainCalls != 4 || reviewCalls != 2 {
					t.Fatalf("unknown review continued or qualified: %+v %+v %d/%d %v/%v", r.Outcome, actual, mainCalls, reviewCalls, e, readErr)
				}
				return
			}
			wantReviews := 2
			if mode == "review" || mode == "next" {
				wantReviews = 4
			}
			if e != nil || readErr != nil || r.Outcome.Status != outcome.Stopped || !actual.Qualified() || actual.Attempts != 2 || mainCalls != len(calls) || reviewCalls != wantReviews {
				t.Fatalf("handoff outcome=%+v task=%+v calls=%d/%d errors=%v/%v", r.Outcome, actual, mainCalls, reviewCalls, e, readErr)
			}
			assertQualifiedTaskWithOpenParents(t, r, s)
			if mode == "next" {
				other, e := plan.GetTask(s, nextTask.ID)
				if e != nil || !other.Qualified() || other.Attempts != 1 || r.Continuation.Assignment.TaskID != other.ID {
					t.Fatalf("next actual task lost authority/validation: %+v %v", other, e)
				}
			}
			b, _ := os.ReadFile("marker")
			if string(b) != "good" {
				t.Fatalf("actual marker %q", b)
			}
			events, e := session.Read(sink.dir, "phases")
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = session.Restore(events, 32768); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestMainPlanningWithoutConfiguredCheck(t *testing.T) {
	for _, mode := range []string{"budget", "alert"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			if e := os.WriteFile("project-check.txt", []byte("inspect before proposing\n"), 0600); e != nil {
				t.Fatal(e)
			}
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
			cfg.Templates.Path = assignedTemplates()
			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				var input struct{ NormalFinal bool }
				_ = json.Unmarshal([]byte(q.Context), &input)
				if mode == "alert" && input.NormalFinal && len(q.Options) == 5 {
					return 4
				}
				return 0
			})
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Tools    []struct{ Function struct{ Name string } }
					Messages []struct{ Content string }
				}
				if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				names := map[string]bool{}
				for _, d := range body.Tools {
					names[d.Function.Name] = true
				}
				for _, name := range []string{"read", "find_file", "find_dir", "help", "grep", "graphql", "task_start", "ask"} {
					if !names[name] {
						t.Errorf("missing legal planning tool %s", name)
					}
				}
				for _, name := range []string{"edit", "write", "bash", "check"} {
					if names[name] {
						t.Errorf("draft phase exposes effect %s", name)
					}
				}
				delta, finish := map[string]any{"content": "check proposal remains unclaimed"}, "stop"
				if calls == 0 {
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "read-project", "type": "function", "function": map[string]string{"name": "read", "arguments": `{"path":"project-check.txt"}`}}}}
					finish = "tool_calls"
				} else {
					seen := false
					for _, m := range body.Messages {
						seen = seen || strings.Contains(m.Content, "inspect before proposing")
					}
					if !seen {
						t.Error("actual project discovery lost before check proposal")
					}
				}
				calls++
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer srv.Close()
			cfg.Model.Server = srv.URL
			r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "discovery", Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: map[string]int{"budget": 2, "alert": 4}[mode]}, "inspect the actual project check")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = r.Run(context.Background(), "inspect the actual project check")
			wantCalls := 2
			if mode == "alert" {
				wantCalls = 4
			}
			if e != nil || r.Outcome.Status != outcome.Stopped || calls != wantCalls || r.Continuation.Coding.Passed() {
				t.Fatalf("unclaimed discovery outcome=%+v calls=%d error=%v", r.Outcome, calls, e)
			}

			if mode == "alert" && strings.Contains(r.Outcome.Reason, "PAT/product enforcement stopped") {
				t.Fatalf("repair advice became stop: %+v", r.Outcome)
			}
		})
	}
}
