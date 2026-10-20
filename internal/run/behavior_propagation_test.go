package run

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
	"sync"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

type behaviorLog struct{ events []session.Event }

func (l *behaviorLog) Append(_ context.Context, _, kind, text string) error {
	l.events = append(l.events, session.Event{Kind: kind, Text: text})
	return nil
}

// These are labelled model/judge HTTP doubles. Registry validation, native Kuzu,
// Bash artifact creation, declared check and reviewer qualification are real.
func TestBehaviorPropagationCapturedRoles(t *testing.T) {
	for _, lane := range []string{"parent registry", "public headless"} {
		t.Run(lane, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			behaviorPath, artifact := filepath.Join(dir, "behavior"), filepath.Join(dir, "artifact")
			raw := strings.Repeat("界", 40) + "R26_RULE_α"
			if err := os.WriteFile(behaviorPath, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Behavior.Path, cfg.Behavior.MaxTokens, cfg.Behavior.CharsPerToken = behaviorPath, 3, 4
			want, _ := agent.ShapeBehavior(raw, 3, 4)
			cfg.Model.Name, cfg.Model.Runtime, cfg.Model.ToolProtocol = "component", "openai", "structured"
			cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
			cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
			cfg.Subagents.Mode, cfg.Workflows.VerifyFollowup, cfg.Workflows.MaxRetries = config.SubagentsSequential, true, 0
			cfg.Context.Window, cfg.Templates.Path = 32768, "../../templates"
			cfg.Coding.CheckCommand = fmt.Sprintf("test \"$(cat '%s')\" = artifact", artifact)
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			root, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			style, err := memory.AppendStyleguideRule(ctx, s, root, "explicit-user", "STYLE_ROLE_RULE: keep artifact checks explicit.")
			if err != nil {
				t.Fatal(err)
			}
			styleBlock, err := prompts.Builtin().Render("continuation.project_style", map[string]any{"Project": root, "Revision": style.ID, "Body": style.Body})
			if err != nil {
				t.Fatal(err)
			}
			want += "\n\n" + styleBlock
			p, err := plan.CreatePlanContext(plan.WithProject(ctx, root), s, "p", "create artifact")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
				t.Fatal(err)
			}
			a, _ := plan.CreateApproach(s, p.ID, "a")
			task, _ := plan.CreateTask(s, a.ID, "artifact", "WORKER_ROLE create the artifact")
			if _, err := plan.SetTaskBlock(s, task.ID, "absent", "artifact", "write file", "declared check exit code 0", "file check", "Review artifact and actual declared check", cfg.Coding.CheckCommand, cfg.Coding.CheckCommand); err != nil {
				t.Fatal(err)
			}
			// Mechanical readiness fixture; actual launch coherence uses the HTTP judge.
			if _, err := plan.Ready(ctx, s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
				t.Fatal(err)
			}
			var parentTask *plan.Task
			if lane == "parent registry" {
				parentTask = assignedFixtureTask(t, s, cfg.Coding.CheckCommand)
			}
			var mu sync.Mutex
			seen := map[string][][]llm.Message{}
			changed := false
			sel := decision.SelectionFor(cfg.Decision)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: sel}, "backends": []string{"cpu"}})
					return
				case "/judge":
					var q decision.Request
					if err := json.NewDecoder(r.Body).Decode(&q); err != nil || len(q.Options) == 0 {
						t.Errorf("invalid judge request: %v", err)
						http.Error(w, "invalid request", 400)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": q.Options[0], "confidence": 1, "model": "component", "device": "cpu"})
					return
				case "/chat/completions":
				default:
					t.Errorf("unbound route %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				var b struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
					t.Error(err)
					return
				}
				role := "worker"
				for _, m := range b.Messages {
					if m.Role == "user" {
						if strings.HasPrefix(m.Content, "Review ") {
							role = "reviewer"
						} else if m.Content == "PARENT_ROLE" || m.Content == "RELOAD_ROLE" {
							role = "parent"
						}
						break
					}
				}
				mu.Lock()
				seen[role] = append(seen[role], b.Messages)
				n := len(seen[role])
				if !changed {
					changed = true
					if err := os.WriteFile(behaviorPath, []byte("LATER_RULE_β"), 0600); err != nil {
						t.Error(err)
					}
				}
				mu.Unlock()
				reply := llm.Message{Role: "assistant", Content: "artifact completed"}
				finish := "stop"
				call := func(name, args string) {
					reply.Content = ""
					reply.ToolCalls = []llm.ToolCall{{ID: fmt.Sprintf("%s-%d", role, n), Name: name, Arguments: args}}
					finish = "tool_calls"
				}
				switch role {
				case "parent":
					if n == 1 {
						args, _ := json.Marshal(map[string]string{"approach": a.ID})
						call("launch", string(args))
					} else if n == 2 {
						args, _ := json.Marshal(map[string]string{"task": parentTask.ID})
						call("task_start", string(args))
					} else if n == 3 {
						call("check", `{}`)
					}
				case "worker":
					if n == 1 {
						args, _ := json.Marshal(map[string]string{"cmd": fmt.Sprintf("printf artifact > '%s'", artifact)})
						call("bash", string(args))
					}
				case "reviewer":
					if n == 1 {
						args, _ := json.Marshal(map[string]string{"cmd": cfg.Coding.CheckCommand})
						call("check", string(args))
					} else {
						reply.Content = "PASS: actual declared artifact check"
					}
				}
				delta := map[string]any{"role": "assistant", "content": reply.Content}
				if len(reply.ToolCalls) > 0 {
					c := reply.ToolCalls[0]
					delta["tool_calls"] = []any{map[string]any{"index": 0, "id": c.ID, "type": "function", "function": map[string]string{"name": c.Name, "arguments": c.Arguments}}}
				}
				chunk, _ := json.Marshal(map[string]any{"model": "component", "choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 5}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer srv.Close()
			cfg.Model.Server, cfg.Decision.Server = srv.URL, srv.URL
			sink := &behaviorLog{}
			if lane == "parent registry" {
				bindDecisionFixture(t, &cfg, nil)
				parent, notes, e := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 5, In: strings.NewReader(""), Out: io.Discard, Session: sink, SessionID: "parent"}, "PARENT_ROLE")
				if e != nil {
					t.Fatal(e)
				}
				if len(notes) < 1 {
					t.Fatal("truncation undisclosed")
				}
				if _, _, e = parent.Run(ctx, "PARENT_ROLE"); e != nil {
					t.Fatalf("parent check/launch failed %+v %v", parent.Outcome, e)
				}
				assertQualifiedTaskWithOpenParents(t, parent, s)
				c, _, e := session.Restore(sink.events, 32768)
				if e != nil {
					t.Fatal(e)
				}
				bindDecisionFixture(t, &cfg, nil)
				next, _, e := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 1, Continuation: c, In: strings.NewReader(""), Out: io.Discard}, "RELOAD_ROLE")
				if e != nil {
					t.Fatal(e)
				}
				// This extra forward proves fresh injection after durable continuation restore;
				// maxSteps=1 is deliberately not another coding-completion qualification.
				if _, _, e = next.Run(ctx, "RELOAD_ROLE"); e != nil {
					t.Fatal(e)
				}
			} else {
				d, e := LaunchDeps(cfg, s, srv.URL, "component", 5, 32768, strings.NewReader(""), io.Discard)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = launch.CheckReady(ctx, d, a.ID); e != nil {
					t.Fatal(e)
				}
				result, e := launch.Launch(ctx, d, a.ID)
				if e != nil || len(result) != 1 || !result[0].Qualified() {
					t.Fatalf("headless check/review failed %+v %v", result, e)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for role, calls := range seen {
				for i, msgs := range calls {
					expected := want
					if role == "parent" && i == len(calls)-1 && lane == "parent registry" {
						expected = "LATER_RULE_β\n\n" + styleBlock
					}
					count := 0
					for _, m := range msgs {
						if m.Role == "system" && m.Content == expected {
							count++
						}
						if m.Content == want && expected != want {
							t.Error("stale behavior resurrected")
						}
					}
					if count != 1 {
						t.Errorf("%s request%d behavior count%d want exact %q", role, i+1, count, expected)
					}
				}
			}
			reviewers := 2
			if lane == "parent registry" {
				reviewers += 1
			} // assigned main adds the inspect-only reviewer turn after the child’s two-turn review
			if len(seen["worker"]) != 2 || len(seen["reviewer"]) != reviewers {
				t.Fatalf("roles not exercised %v", seen)
			}
			got, e := plan.GetTask(s, task.ID)
			if e != nil || !got.Qualified() || got.Attempts != 1 || !strings.Contains(got.AttemptLog, "R26_RULE_α") || !strings.Contains(got.AttemptLog, "\"ExitCode\":0") {
				t.Errorf("role/check attempt evidence lost qualified=%v attempts=%d err=%v", got.Qualified(), got.Attempts, e)
			}
			bytes, e := os.ReadFile(artifact)
			if e != nil || string(bytes) != "artifact" {
				t.Fatalf("actual artifact %q %v", bytes, e)
			}
		})
	}
}
