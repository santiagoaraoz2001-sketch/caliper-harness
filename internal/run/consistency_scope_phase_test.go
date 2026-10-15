package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestConsistencyCommentReceiptUsesCurrentRunnerSink(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
	cfg.Templates.Path = assignedTemplates()
	bindDecisionFixture(t, &cfg, nil)
	task := assignedFixtureTask(t, s, "true")
	old := &codingSink{dir: filepath.Join(dir, "original-sink")}
	r, _, err := New(Deps{Cfg: cfg, Store: s, Session: old, SessionID: "original", Server: "http://unused.invalid", Model: "component", Window: 32768}, "perform declared work")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.Assigned.Start(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	comment, err := plan.AppendTaskComment(plan.WithProject(t.Context(), root), s, task.ID, 1, "user", "authored-session", "current sink advice δ🙂")
	if err != nil {
		t.Fatal(err)
	}
	current := &codingSink{dir: filepath.Join(dir, "current-sink")}
	oldText, err := os.ReadFile(filepath.Join(old.dir, "original.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r.Session, r.SessionID = current, "swapped"
	if r.CommentReceipt == nil {
		t.Fatal("direct runner has no owning durable comment receipt")
	}
	if err = r.CommentReceipt(t.Context(), *comment); err != nil {
		t.Fatal(err)
	}
	events, err := session.Read(current.dir, "swapped")
	if err != nil || len(events) != 1 || events[0].Kind != "task_comment" {
		t.Fatalf("delivery missed current runner sink/ID: %+v %v", events, err)
	}
	var saved outcome.TaskComment
	if err = json.Unmarshal([]byte(events[0].Text), &saved); err != nil || saved != *comment {
		t.Fatalf("current raw receipt changed comment provenance: %+v %v", saved, err)
	}
	afterOld, err := os.ReadFile(filepath.Join(old.dir, "original.jsonl"))
	if err != nil || string(afterOld) != string(oldText) {
		t.Fatalf("delivery appended to old captured sink: %v", err)
	}
	actual, err := plan.GetTask(s, task.ID)
	if err != nil || !strings.Contains(actual.AttemptLog, `"SessionRef":"swapped"`) || strings.Contains(actual.AttemptLog, `"SessionRef":"original"`) {
		t.Fatalf("owning delivery record missed current recipient: %+v %v", actual, err)
	}
}

func consistencyRunner(t *testing.T, f *planningFixture, scope *session.WorkflowScope, exposure string) *agent.Runner {
	t.Helper()
	t.Chdir(f.d.Workspace)
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
	cfg.Templates.Path = assignedTemplates()
	r, _, err := New(Deps{Tools: exposure, Cfg: cfg, Store: f.d.Store, Server: "http://unused.invalid", Model: "component", Window: 32768, Continuation: session.Continuation{Workflow: scope}}, "deliver the declared work")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Actual native requests cover both phase filters: unclaimed, admitted main
// execution, and settled metadata. Checks are real; model/reviewer are doubles.
func TestConsistencyNativeHeadersMatchCurrentClaimPhase(t *testing.T) {
	for _, exposure := range []string{"coding", "full", "workflow"} {
		t.Run(exposure, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			task := assignedFixtureTask(t, s, "true", "test -d .")
			scope := selectedFixtureScope(t, s, task.ID)
			mainCalls, reviews := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				review := false
				for _, message := range body.Messages {
					review = review || strings.HasPrefix(message.Content, "Review ")
				}
				delta, reason := map[string]any{"reasoning": "stop with actual artifacts preserved"}, "stop"
				if review {
					reviews++
					delta = map[string]any{"content": "PASS: mechanical component episode only"}
				} else {
					names := []string{}
					for _, definition := range body.Tools {
						names = append(names, definition.Function.Name)
					}
					sort.Strings(names)
					matched := false
					for _, message := range body.Messages {
						if message.Role == "system" && strings.Contains(message.Content, "Selected workflow:") {
							matched = strings.Contains(message.Content, "legal tools: "+strings.Join(names, ", ")+".")
						}
					}
					if !matched {
						t.Errorf("native header mismatches actual tools at phase%d: %v", mainCalls, names)
					}
					if exposure != "coding" && mainCalls == 0 {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "actual-claim", "type": "function", "function": map[string]string{"name": "task_start", "arguments": fmt.Sprintf(`{"task":%q}`, task.ID)}}}}
						reason = "tool_calls"
					} else if exposure != "coding" && mainCalls == 1 {
						if !strings.Contains(strings.Join(names, ","), "write") || strings.Contains(strings.Join(names, ","), "task_start") {
							t.Errorf("actual claim projection wrong: %v", names)
						}
						delta = map[string]any{"content": "mechanical role probe complete"}
					} else {
						for _, name := range names {
							if name == "write" || name == "edit" || name == "bash" || name == "check" {
								if exposure != "coding" || mainCalls != 0 {
									t.Errorf("settled request retained %s", name)
								}
							}
						}
					}
					mainCalls++
				}
				raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Templates.Path = assignedTemplates()
			bindDecisionFixture(t, &cfg, nil)
			r, _, err := New(Deps{Tools: exposure, Cfg: cfg, Store: s, Server: server.URL, Model: "component", Window: 32768, MaxSteps: 6, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "phase-wire", Continuation: session.Continuation{Workflow: scope}}, "exercise the declared mechanical episode")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = r.Run(context.Background(), "exercise the declared mechanical episode"); err != nil {
				t.Fatal(err)
			}
			wantMain, wantReviews := 3, 1
			if exposure == "coding" {
				wantMain, wantReviews = 1, 0
			}
			if mainCalls != wantMain || reviews != wantReviews {
				t.Fatalf("actual phase wires main%d reviewer%d want%d/%d", mainCalls, reviews, wantMain, wantReviews)
			}
		})
	}
}

// No model or decision service runs: the public builder, Kuzu and final view
// projection are real. The header must describe the definitions actually sent.
func TestConsistencyWorkflowHeaderUsesFinalRegistry(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	n, err := launch.ScopeNode(f.d.Store, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	ap, err := launch.ScopeNode(f.d.Store, &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: n.Parent})
	if err != nil {
		t.Fatal(err)
	}
	scopes := []*session.WorkflowScope{{Store: f.d.Store.Identity(), Kind: "root"}, {Store: f.d.Store.Identity(), Kind: "plan", ID: ap.Parent}, {Store: f.d.Store.Identity(), Kind: "approach", ID: n.Parent}, f.scope}
	for _, exposure := range []string{"", "coding", "full", "workflow"} {
		for _, scope := range scopes {
			t.Run(exposure+"/"+scope.Kind, func(t *testing.T) {
				r := consistencyRunner(t, f, scope, exposure)
				view, header, err := r.WorkflowView(scope)
				if err != nil {
					t.Fatal(err)
				}
				node, err := launch.ScopeNode(f.d.Store, scope)
				if err != nil {
					t.Fatal(err)
				}
				want := r.Prompts.Format("system.workflow.scope_header", node.Kind, node.Ref, node.ID, node.State, node.Ancestors, node.Children, strings.Join(view.Names(), ", "))
				if header != want {
					t.Fatalf("header contradicts final definitions:\nactual %s\nwant   %s", header, want)
				}
				if exposure == "" || exposure == "coding" {
					for _, name := range []string{"prepare_task", "ready_task", "task_start", "workflow_plan", "workflow_approach", "workflow_task"} {
						if _, available := view.Lookup(name); available {
							t.Fatalf("unclaimed ordinary planning leaked %s", name)
						}
					}
					for _, name := range []string{"write", "edit", "bash", "check"} {
						if _, available := view.Lookup(name); !available {
							t.Fatalf("draft ordinary planning hid %s", name)
						}
					}
				}
				// Snapshot/resume never turns selected metadata into a claim.
				r.Continuation = r.Continuation.Snapshot()
				resumed, resumedHeader, err := r.WorkflowView(r.Continuation.Workflow)
				if err != nil || strings.Join(resumed.Names(), ",") != strings.Join(view.Names(), ",") || resumedHeader != header || r.Continuation.Assignment != nil {
					t.Fatalf("resume projection changed authority/header: %v %s", err, resumedHeader)
				}
			})
		}
	}
}

// Answers mutate the real graph while the Runner retains its previous request's
// registry. Advice must re-read the current question, not replay that registry.
func TestConsistencyPlanningNextActionTracksEachAnsweredQuestion(t *testing.T) {
	f := newPlanningFixture(t)
	f.call(tool.Args{})
	r := consistencyRunner(t, f, f.scope, "coding")
	answers := []tool.Args{
		{"title": "Repair", "outcome": "requested behavior", "rules": "preserve neighbors", "articles": "first change", "proof": "observed regression"},
		{"strategy": "repair owner with regression"},
		{"title": "first task", "objective": "change requested behavior"},
		{"current": "observed defect", "target": "correct behavior"},
		{"acceptance": "assert correct behavior", "tests": "planned assertion; unexecuted", "acceptance_command": "test 1 -eq 1"},
		{"objective": "necessary bounded purpose"},
		{"execution": "change responsible implementation"},
		{"validation": "distinguishing assertion"},
		{"more": false},
		{"verify": "inspect implementation and rerun assertion independently"},
		{"invoke": "COMPLETE"},
	}
	for _, answer := range answers {
		f.call(answer)
		r.Continuation.Workflow = f.scope
		actual, err := r.Assigned.Facts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		facts, ok := actual.(agent.BookkeepingFacts)
		if !ok {
			t.Fatalf("unexpected owning facts %T", actual)
		}
		current := f.definition()
		if facts.Preparation != current.Schema() {
			t.Errorf("current question lost after %v:\nactual %s\nwant   %s", answer, facts.Preparation, current.Schema())
		}
		if len(current.Params) == 1 && current.Params[0].Name == "strategy" && (strings.Contains(facts.Preparation, "COMPLETE") || strings.Contains(facts.Preparation, "EXECUTE")) {
			t.Error("strategy question prematurely directs readiness/execution")
		}
	}
	if f.judgments != 1 {
		t.Fatalf("phase advice ran readiness again: judgments=%d", f.judgments)
	}
}
