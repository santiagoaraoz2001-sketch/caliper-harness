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

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// Model/judge responses are controlled; binding, whole-array admission, Kuzu,
// readiness, claim, continuation publication and the next HTTP request are real.
func TestPreparationRecoveryCurrentNextWire(t *testing.T) {
	for _, protocol := range []string{"structured", "text"} {
		for _, sequence := range []string{"tool refusal", "normal-final PAT repair", "normal-final combined repair", "coherent final fallback"} {
			t.Run(protocol+"/"+sequence, func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				if err := os.WriteFile("source.go", []byte("package source\n"), 0600); err != nil {
					t.Fatal(err)
				}
				s, err := store.Open(filepath.Join(dir, "db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				cfg := config.Defaults()
				cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
				cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", protocol
				cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
				pending, judgments := "", 0
				var active *agent.Runner
				pc, err := cfg.LoadPrompts()
				if err != nil {
					t.Fatal(err)
				}
				bindDecisionFixture(t, &cfg, func(q decision.Request) int {
					if len(q.Options) != 5 {
						return 0
					}
					var input struct {
						PendingRepair string
						Facts         struct{ Preparation string }
					}
					if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
						t.Error(err)
					}
					if input.PendingRepair != pending {
						t.Errorf("next real judge request lost exact PendingRepair: got %q want %q", input.PendingRepair, pending)
					}
					judgments++
					if sequence == "coherent final fallback" {
						pending = ""
						return 0
					}
					option, pick := "request executor PAT update", 1
					if sequence == "normal-final combined repair" {
						option, pick = "request both PAT and product knowledge updates", 3
					}
					pending = pc.Text("continuation.bookkeeping.request_prefix") + option + pc.Text("continuation.bookkeeping.authority_suffix") + pc.Text("continuation.bookkeeping.next_action") + input.Facts.Preparation
					return pick
				})
				n, taskID := 0, ""
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					var body struct{ Messages []llm.Message }
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					for _, m := range body.Messages {
						if at := strings.Index(m.Content, `{"data":`); at >= 0 {
							var result graph.Result
							if json.Unmarshal([]byte(m.Content[at:]), &result) == nil && len(result.Effects) == 4 {
								taskID = result.Effects[3].ID
							}
						}
					}
					var current []string
					legacy, source := false, false
					for _, m := range body.Messages {
						if strings.HasPrefix(m.Content, pc.Text("continuation.preparation.current_frame")) {
							current = append(current, m.Content)
						}
						legacy = legacy || m.Content == "Legacy guidance: preserve unresolved knowledge requirement"
						source = source || strings.Contains(m.Content, "package source")
					}
					if !legacy {
						t.Error("ambiguous legacy guidance was removed")
					}
					if n >= 2 && n <= 5 {
						if len(current) != 1 {
							t.Errorf("request%d current preparation receipts=%d, want1", n+1, len(current))
						} else {
							text := current[0]
							facts, err := active.Assigned.Facts(req.Context())
							if err != nil {
								t.Error(err)
								return
							}
							currentFacts := facts.(agent.BookkeepingFacts)
							if !strings.Contains(currentFacts.Preparation, "tool: plan") || n >= 4 && currentFacts.Checkpoint.SelectedID != taskID {
								t.Errorf("request%d actual current owner facts are wrong: %+v", n+1, currentFacts.Checkpoint)
							}
							if sequence != "coherent final fallback" && !strings.Contains(text, currentFacts.Preparation) {
								t.Errorf("request%d stale recovery: %q", n+1, text)
							}
							if n == 2 && sequence == "tool refusal" {
								retained := false
								for _, message := range body.Messages {
									if protocol == "structured" && message.Role == "tool" && message.ToolCallID == "recovery1" || protocol == "text" && message.Role == "user" {
										retained = retained || strings.Contains(message.Content, "checkCommand must equal configured command")
									}
								}
								if !retained {
									t.Error("actual refusal receipt was lost from model context")
								}
							}
							currentScope := false
							for _, m := range body.Messages {
								currentScope = currentScope || strings.Contains(m.Content, "Selected workflow: task ") && strings.Contains(m.Content, taskID)
							}
							if sequence == "coherent final fallback" && n >= 4 && !currentScope {
								t.Error("historical preparation guidance superseded the actual current task role")
							}
							if n >= 3 && strings.Contains(text, "No unfinished same-project plan exists") {
								t.Error("fresh-PAT recipe survived actual creation")
							}
						}
						if !source {
							t.Error("source observation disappeared")
						}
					}
					if n == 2 {
						plans, err := plan.ListPlans(s, "")
						if err != nil || len(plans) != 0 {
							t.Errorf("binding refusal created work: %+v %v", plans, err)
						}
					}
					if n == 6 {
						knowledge, preparationHistory := false, false
						for _, e := range active.Continuation.Entries {
							preparationHistory = preparationHistory || e.PreparationScope != ""
							knowledge = knowledge || e.Kind == "guidance" && e.Round == nil && strings.Contains(e.Message.Content, "request supported product knowledge update")
						}
						if !preparationHistory || len(current) != 1 {
							t.Error("claim deleted historical preparation advice")
						}
						if sequence == "normal-final combined repair" && !knowledge {
							t.Error("combined normal-final request lost unresolved product knowledge")
						}
						if !strings.Contains(body.Messages[0].Content, "EXECUTE claimed task "+taskID) {
							t.Error("current system role did not supersede historical preparation advice")
						}
					}
					name, args := "read", map[string]any{"path": "source.go"}
					if n == 1 || n == 2 {
						name = "prepare_task"
						args = map[string]any{"plan_title": "repair", "plan_goal": "correct source", "approach_title": "source fix", "task_title": "repair", "task_prompt": "repair source", "current": "observed source", "target": "correct source", "sketch": "inspect and repair owner", "acceptance": "true exits0", "tests": "existing source tests", "verify": "inspect source and actual checks", "check_command": "true", "acceptance_command": "test -f source.go"}
						if n == 1 {
							args["check_command"] = "false"
						}
					}
					if n == 3 {
						name, args = "workflow_focus", map[string]any{"ref": taskID}
					}
					if n == 4 || n == 5 {
						name, args = "ready_task", map[string]any{"task": taskID}
						if n == 5 {
							name = "task_start"
						}
					}
					delta, finish := map[string]any{"reasoning": "component stop; no claimed correctness"}, "stop"
					if n == 1 && sequence != "tool refusal" {
						delta = map[string]any{"content": "normal final: inspected source; no implementation or claim yet"}
					} else if n < 6 {
						if protocol == "structured" {
							raw, _ := json.Marshal(args)
							delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("recovery", n), "type": "function", "function": map[string]string{"name": name, "arguments": string(raw)}}}}
							finish = "tool_calls"
						} else {
							text := "```tool\ntool: " + name + "\n"
							for k, v := range args {
								text += fmt.Sprintf("%s: %v\n", k, v)
							}
							delta = map[string]any{"content": text + "```"}
						}
					}
					n++
					frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
				}))
				defer server.Close()
				cfg.Model.Server = server.URL
				initial := session.Continuation{Version: 1, Workflow: selectedFixtureScope(t, s, "root")}
				initial.Add("intent", "user", "repair source without changing the brief", false)
				initial.AddGuidance("Legacy guidance: preserve unresolved knowledge requirement", nil)
				sink := &codingSink{dir: filepath.Join(dir, "sessions")}
				r, _, err := New(Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: server.URL, Model: "component", Session: sink, SessionID: "recovery", Continuation: initial, Window: 32768, MaxSteps: 7}, "repair source")
				if err != nil {
					t.Fatal(err)
				}
				active = r
				if _, _, err = r.Run(context.Background(), "repair source"); err != nil {
					t.Fatal(err)
				}
				actual, err := plan.GetTask(s, taskID)
				if judgments < 2 {
					t.Error("next actual decision request was not observed")
				}
				if n != 7 || err != nil || actual.Attempts != 1 || actual.State != plan.TaskStopped || actual.Qualified() {
					t.Fatalf("authority changed: requests%d task%+v err%v", n, actual, err)
				}
				events, err := session.Read(sink.dir, "recovery")
				if err != nil {
					t.Fatal(err)
				}
				restored, _, err := session.Restore(events, 32768)
				if err != nil || restored.Validate() != nil {
					t.Fatalf("durable restore: %v", err)
				}
				if sequence != "tool refusal" {
					logged := false
					for _, e := range events {
						if e.Kind == "enforcement" && (sequence == "coherent final fallback" && e.Text == pc.Text("continuation.no_claim_final") || sequence != "coherent final fallback" && strings.Contains(e.Text, strings.TrimSpace(pc.Text("continuation.bookkeeping.next_action")))) {
							logged = true
						}
					}
					if !logged {
						t.Error("original normal-final repair/fallback raw event missing")
					}
				}
				if protocol == "structured" {
					results := 0
					for _, e := range events {
						if e.Kind == "native_result" {
							results++
						}
					}
					wantResults := 6
					if sequence != "tool refusal" {
						wantResults = 5
					}
					if results != wantResults {
						t.Fatalf("raw results lost: %d", results)
					}
				}
			})
		}
	}
}

func TestPreparationRecoveryPartialEffectsNextWire(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	pc, err := cfg.LoadPrompts()
	if err != nil {
		t.Fatal(err)
	}
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		if len(q.Options) == 5 {
			return 1
		}
		return 0
	})
	n, planID, taskID := 0, "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []llm.Message }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		var latest graph.Result
		for _, m := range body.Messages {
			if m.Role == "tool" {
				if at := strings.Index(m.Content, `{"data":`); at >= 0 {
					if err := json.Unmarshal([]byte(m.Content[at:]), &latest); err != nil {
						t.Error(err)
					}
				}
				if len(latest.Effects) == 4 {
					planID, taskID = latest.Effects[0].ID, latest.Effects[3].ID
				}
			}
		}
		if n == 2 {
			if len(latest.Errors) == 0 || len(latest.Effects) != 2 || latest.Effects[0].Status != "applied" || latest.Effects[0].ID != planID || latest.Effects[1].Status == "applied" {
				t.Errorf("partial effects were lost or rolled back: %+v", latest)
			}
			last := body.Messages[len(body.Messages)-1].Content
			if !strings.HasPrefix(last, pc.Text("continuation.preparation.current_frame")) || !strings.Contains(last, planID) {
				t.Errorf("partial-effect recovery absent: %q", last)
			}
			if strings.Contains(last, "partial or unknown effects require owning readback") {
				t.Errorf("actual partial receipt became a compulsory readback chore: %q", last)
			}
			actual, err := plan.GetPlan(s, planID)
			if err != nil || actual.State != plan.PlanActive {
				t.Errorf("actual applied activation lost: %+v %v", actual, err)
			}
		}
		delta, finish := map[string]any{"reasoning": "stop with partial metadata preserved"}, "stop"
		if n < 2 {
			name, args := "prepare_task", map[string]any{"plan_title": "partial", "plan_goal": "inspect actual effects", "approach_title": "source fix", "task_title": "repair", "task_prompt": "repair source", "current": "observed source", "target": "correct source", "sketch": "inspect and repair owner", "acceptance": "true exits0", "tests": "source tests", "verify": "inspect actual source and checks", "check_command": "true", "acceptance_command": "test -d ."}
			if n == 1 {
				name, args = "graphql", map[string]any{"query": fmt.Sprintf(`mutation { applied:transitionPlan(id:%q,to:"active"){id state} failed:retirePlan(id:"missing"){id state} }`, planID)}
			}
			raw, _ := json.Marshal(args)
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("partial", n), "type": "function", "function": map[string]string{"name": name, "arguments": string(raw)}}}}
			finish = "tool_calls"
		}
		n++
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer server.Close()
	cfg.Model.Server = server.URL
	sink := &codingSink{dir: filepath.Join(dir, "sessions")}
	r, _, err := New(Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: server.URL, Model: "component", Session: sink, SessionID: "partial", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Window: 32768, MaxSteps: 3}, "inspect partial effects")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.Run(context.Background(), "inspect partial effects"); err != nil {
		t.Fatal(err)
	}
	actual, err := plan.GetTask(s, taskID)
	if n != 3 || err != nil || actual.Attempts != 0 || actual.State != plan.TaskQueued || actual.Qualified() || r.Continuation.Workflow == nil || r.Continuation.Workflow.Kind != "root" || r.Continuation.Workflow.Store != s.Identity() || r.Continuation.Assignment != nil {
		t.Fatalf("partial effects granted authority: %+v %v", actual, err)
	}
}
