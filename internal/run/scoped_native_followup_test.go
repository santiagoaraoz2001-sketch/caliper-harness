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

	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestScopedHostConfiguredCheckUsesOwningDefinition(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("failed=", fail), func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			scope, e := launch.SelectScope(s, "root")
			if e != nil {
				t.Fatal(e)
			}
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["completion","tools","thinking"]}`)
					return
				}
				if req.URL.Path != "/api/chat" {
					t.Errorf("unexpected origin/path %s", req.URL.Path)
					http.Error(w, "wrong", 400)
					return
				}
				var body struct {
					Tools []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
					Stream   bool `json:"stream"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				review := false
				for _, m := range body.Messages {
					review = review || strings.HasPrefix(m.Content, "Review ")
				}
				if requests == 2 && !fail {
					progress := false
					for _, m := range body.Messages {
						progress = progress || strings.Contains(m.Content, "VALIDATED task=")
					}
					if !progress {
						t.Error("qualified host-check progress missing from consuming request")
					}
				}
				if fail && review {
					t.Error("failed host check admitted reviewer")
				}
				for _, d := range body.Tools {
					if d.Function.Name == "launch" && !review {
						t.Error("claimed worker acquired unrelated dispatch schema")
					}
					if d.Function.Name == "check" && !review {
						t.Error("component projection exposed the model check schema")
					}
				}
				if !body.Stream {
					t.Error("native tool route did not stream")
				}
				requests++
				json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "message": map[string]any{"role": "assistant", "content": "PASS complete", "thinking": "original thought"}, "done": true, "done_reason": "stop", "prompt_eval_count": 100, "eval_count": 5})
			}))
			defer srv.Close()
			cfg := config.Defaults()
			cfg.Model.Server, cfg.Model.Runtime, cfg.Decision.Server = srv.URL, "ollama", srv.URL
			cfg.Auth.Mode = config.AuthAll
			cfg.Recall.TopN = 0
			cfg.Coding.CheckCommand = "printf host >> marker; pwd > checked-cwd"
			if fail {
				cfg.Coding.CheckCommand += "; exit 9"
			}
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			approvals := 0
			bindDecisionFixture(t, &cfg, nil)
			r, _, e := assignedCodingNew(t, Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", MaxSteps: 3, Window: 32768, Session: sink, SessionID: "scoped-host", Continuation: session.Continuation{Workflow: scope}, AskYesNo: func(_ context.Context, p string) bool {
				approvals++
				if _, e := os.Stat("marker"); !os.IsNotExist(e) {
					t.Error("effect before approval")
				}
				events, e := session.Read(sink.dir, "scoped-host")
				if e != nil {
					t.Error(e)
				}
				announced := false
				for _, ev := range events {
					announced = announced || ev.Kind == "host_check"
				}
				if !announced {
					t.Error("no durable host announcement before approval")
				}
				_, body, ok := strings.Cut(p, "Complete validated action (JSON):\n")
				var a struct {
					Tool string
					Args map[string]any
				}
				return ok && json.Unmarshal([]byte(body), &a) == nil && a.Tool == "check" && a.Args["cmd"] == cfg.Coding.CheckCommand
			}}, "claim needs actual scoped check")
			if e != nil {
				t.Fatal(e)
			}
			if _, ok := r.Registry.Lookup("check"); ok {
				t.Fatal("draft registry exposes check before claim")
			}
			// Component-only projection double: keep check hidden across claim/refresh.
			// The factory's retained definition, approvals, commands and receipts are real.
			owningView := r.WorkflowView
			r.WorkflowView = func(selected *session.WorkflowScope) (*tool.Registry, string, error) {
				view, header, err := owningView(selected)
				if err != nil {
					return nil, "", err
				}
				names := []string{}
				for _, name := range view.Names() {
					if name != "check" {
						names = append(names, name)
					}
				}
				view = view.View(names)
				if selected != nil {
					n, err := launch.ScopeNode(s, selected)
					if err != nil {
						return nil, "", err
					}
					header = r.Prompts.Format("system.workflow.scope_header", n.Kind, n.Ref, n.ID, n.State, n.Ancestors, n.Children, strings.Join(view.Names(), ", "))
				}
				return view, header, nil
			}
			r.Registry, _, e = r.WorkflowView(r.Continuation.Workflow)
			if e != nil {
				t.Fatal(e)
			}
			if _, ok := r.Registry.Lookup("check"); ok {
				t.Fatal("component model registry exposes check")
			}
			final, _, runErr := r.Run(context.Background(), "claim needs actual scoped check")
			marker, _ := os.ReadFile("marker")
			cwd, _ := os.ReadFile("checked-cwd")
			want := outcome.Completed
			wantRequests := 3 // main, independent reviewer, then qualified-progress consumption
			if fail {
				wantRequests = 2 // bounded remediation request; failed check never admits review
				want = outcome.Failed
			}
			if string(marker) != "host" || strings.TrimSpace(string(cwd)) != r.Workspace || approvals != 1 || requests != wantRequests || final != "PASS complete" || r.Outcome.Status != want || r.Continuation.Coding == nil || r.Continuation.Coding.Receipt.ExitCode == nil {
				t.Fatalf("scoped host marker=%q cwd=%q approvals=%d requests=%d final=%q outcome=%+v coding=%+v err=%v", marker, cwd, approvals, requests, final, r.Outcome, r.Continuation.Coding, runErr)
			}
			wantExit := 0
			if fail {
				wantExit = 9
			}
			if *r.Continuation.Coding.Receipt.ExitCode != wantExit {
				t.Fatalf("receipt %+v", r.Continuation.Coding.Receipt)
			}
			events, e := session.Read(sink.dir, "scoped-host")
			if e != nil {
				t.Fatal(e)
			}
			host, raw := 0, false
			for _, ev := range events {
				if ev.Kind == "host_check" {
					host++
				}
				if ev.Kind == "native_reply" {
					raw = raw || strings.Contains(ev.Text, "PASS complete") && strings.Contains(ev.Text, "original thought")
					if strings.Contains(ev.Text, "tool_calls") && !strings.Contains(ev.Text, "task_start") {
						t.Error("host fabricated native call")
					}
				}
			}
			if host != 1 || !raw {
				t.Fatalf("host=%d raw=%t", host, raw)
			}
			restored, _, e := session.Restore(events, 32768)
			if e != nil || restored.Coding.Passed() {
				t.Fatalf("historical receipt restore %+v %v", restored.Coding, e)
			}
		})
	}
}

func TestScopedCreationWireExplicitFocusAndGuide(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	scope, e := launch.SelectScope(s, "root")
	if e != nil {
		t.Fatal(e)
	}
	type request struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
	}
	var requests []request
	var ids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/show" {
			fmt.Fprint(w, `{"capabilities":["completion","tools","thinking"]}`)
			return
		}
		if req.URL.Path != "/api/chat" {
			t.Errorf("wrong actual origin/path %s", req.URL.Path)
			http.Error(w, "wrong", 400)
			return
		}
		var body request
		if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		requests = append(requests, body)
		name := ""
		args := map[string]any{}
		switch len(requests) {
		case 1:
			name = "workflow_plan"
			args = map[string]any{"title": "owned plan", "goal": "actual two-level preparation"}
		case 2:
			ps, e := plan.ListPlans(s, "")
			if e != nil || len(ps) != 1 {
				t.Errorf("plans %v %v", ps, e)
				return
			}
			ids = append(ids, ps[0].ID)
			name = "workflow_focus"
			args["ref"] = ids[0]
		case 3:
			name = "workflow_approach"
			args["title"] = "owned approach"
		case 4:
			as, e := plan.PlanApproaches(s, ids[0])
			if e != nil || len(as) != 1 {
				t.Errorf("approaches %v %v", as, e)
				return
			}
			ids = append(ids, as[0].ID)
			name = "workflow_focus"
			args["ref"] = ids[1]
		case 5:
			name = "workflow_task"
			args = map[string]any{"title": "owned task", "prompt": "complete frozen instructions"}
		case 6:
			ts, e := plan.ApproachTasks(s, ids[1])
			if e != nil || len(ts) != 1 {
				t.Errorf("tasks %v %v", ts, e)
				return
			}
			ids = append(ids, ts[0].ID)
			name = "workflow_focus"
			args["ref"] = ids[2]
		}
		message := map[string]any{"role": "assistant", "content": "prepared only"}
		if name != "" {
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": name, "arguments": args}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "message": message, "done": true, "done_reason": "stop", "prompt_eval_count": 100, "eval_count": 5})
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server, cfg.Model.Runtime, cfg.Decision.Server = srv.URL, "ollama", srv.URL
	cfg.Recall.TopN = 0
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Coding.CheckCommand = ""
	cfg.Behavior.Path = filepath.Join(dir, "behavior")
	if e := os.WriteFile(cfg.Behavior.Path, []byte("ONE_BEHAVIOR"), 0600); e != nil {
		t.Fatal(e)
	}
	sink := focusDiskLog{dir: filepath.Join(dir, "sessions")}
	bindDecisionFixture(t, &cfg, nil)
	deps := Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", MaxSteps: 7, Window: 32768, Session: sink, SessionID: "chain", Continuation: session.Continuation{Workflow: scope}}
	r, _, e := New(deps, "ONE_INTENT")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.Run(context.Background(), "ONE_INTENT"); e != nil {
		t.Fatal(e)
	}
	if r.Outcome.Status != outcome.Stopped {
		t.Fatalf("preparation without a declared check qualified: %+v", r.Outcome)
	}
	if len(requests) != 7 || len(ids) != 3 {
		t.Fatalf("requests%d ids%v outcome%+v", len(requests), ids, r.Outcome)
	}
	kinds := []string{"root", "plan", "plan", "approach", "approach", "task", "task"}
	for i, req := range requests {
		names := []string{}
		for _, definition := range req.Tools {
			function, _ := definition["function"].(map[string]any)
			name, _ := function["name"].(string)
			names = append(names, name)
		}
		sort.Strings(names)
		base, behavior, intent := 0, 0, 0
		for _, m := range req.Messages {
			c, _ := m["content"].(string)
			if strings.HasPrefix(c, "You are a professional software engineer") {
				base++
				if !strings.Contains(c, "Selected workflow: "+kinds[i]+" ") || !strings.Contains(c, "legal tools: "+strings.Join(names, ", ")+".") {
					t.Errorf("request%d missing scoped guide %q", i+1, c)
				}
			}
			if c == "ONE_BEHAVIOR" {
				behavior++
			}
			if c == "ONE_INTENT" {
				intent++
			}
		}
		if base != 1 || behavior != 1 || intent != 1 {
			t.Errorf("request%d base%d behavior%d intent%d", i+1, base, behavior, intent)
		}
	}
	for j, index := range []int{1, 3, 5} {
		found := false
		for _, m := range requests[index].Messages {
			if m["role"] == "tool" {
				c, _ := m["content"].(string)
				if strings.Contains(c, "created "+kinds[index+1]+" "+ids[j]) {
					found = true
					if !strings.Contains(c, "Node "+ids[j]+" is selected for the next request") || !strings.Contains(c, "Metadata only") || strings.Contains(c, "workflow_transition(kind=") || strings.Contains(c, "workflow_task(approach=") || strings.Contains(c, "workflow_block(task=") {
						t.Errorf("contradictory scoped creation receipt %q", c)
					}
				}
			}
		}
		if !found {
			t.Errorf("missing actual creation receipt for %s", ids[j])
		}
	}
	events, e := session.Read(sink.dir, "chain")
	if e != nil {
		t.Fatal(e)
	}
	restored, _, e := session.Restore(events, 32768, r.ModelKey)
	if e != nil {
		t.Fatal(e)
	}
	deps.Continuation = restored
	deps.SessionID = "resume"
	deps.MaxSteps = 1 // one consuming request proves restoration does not replay creation
	r2, _, e := New(deps, "AFTER_RESUME")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = r2.Run(context.Background(), "AFTER_RESUME"); e != nil {
		t.Fatal(e)
	}
	if len(requests) != 8 {
		t.Fatalf("resume replayed calls: %d", len(requests))
	}
	ps, _ := plan.ListPlans(s, "")
	as, _ := plan.PlanApproaches(s, ids[0])
	ts, _ := plan.ApproachTasks(s, ids[1])
	if len(ps) != 1 || len(as) != 1 || len(ts) != 1 {
		t.Fatal("resume replayed creation")
	}
	last := requests[7]
	joined := ""
	for _, m := range last.Messages {
		c, _ := m["content"].(string)
		joined += c + "\n"
	}
	if strings.Count(joined, "Plan → Approach → Task") != 1 || strings.Count(joined, "ONE_BEHAVIOR") != 1 || strings.Count(joined, "ONE_INTENT") != 1 {
		t.Fatalf("resume prefix missing or duplicated %q", joined)
	}
}
