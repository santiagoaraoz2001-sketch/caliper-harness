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
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

type focusDiskLog struct{ dir, fail string }

func (s focusDiskLog) Append(_ context.Context, id, kind, text string) error {
	if kind == s.fail {
		return fmt.Errorf("controlled %s failure", kind)
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

// Public factory, actual HTTP, owning graph mutations, approval and disk resume.
func TestWorkflowPublicWireFrozenBatchAndPrefix(t *testing.T) {
	for _, failure := range []string{"", "bound_call", "continuation"} {
		t.Run("record="+failure, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			root, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx := plan.WithProject(context.Background(), root)
			s, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p1, e := plan.CreatePlanContext(ctx, s, "first", "first goal")
			if e != nil {
				t.Fatal(e)
			}
			p2, e := plan.CreatePlanContext(ctx, s, "second", "second goal")
			if e != nil {
				t.Fatal(e)
			}
			for _, p := range []*plan.Plan{p1, p2} {
				if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
					t.Fatal(e)
				}
			}
			var requests []struct {
				Messages []map[string]any `json:"messages"`
				Tools    []map[string]any `json:"tools"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/show" {
					fmt.Fprint(w, `{"capabilities":["completion","tools","thinking"]}`)
					return
				}
				if req.URL.Path != "/api/chat" {
					t.Errorf("unexpected endpoint %s", req.URL.Path)
					http.Error(w, "wrong", 400)
					return
				}
				var body struct {
					Messages []map[string]any `json:"messages"`
					Tools    []map[string]any `json:"tools"`
				}
				if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				requests = append(requests, body)
				call := func(name string, a map[string]any) map[string]any {
					return map[string]any{"function": map[string]any{"name": name, "arguments": a}}
				}
				var calls []map[string]any
				switch len(requests) {
				case 1:
					calls = []map[string]any{call("workflow_focus", map[string]any{"ref": "P1"})}
				case 2:
					calls = []map[string]any{call("workflow_focus", map[string]any{"ref": p2.ID}), call("workflow_approach", map[string]any{"title": "frozen parent"})}
				case 3:
					calls = []map[string]any{call("workflow_focus", map[string]any{"ref": p2.ID})}
				case 4:
					calls = []map[string]any{call("workflow_approach", map[string]any{"title": "next parent"})}
				}
				message := map[string]any{"role": "assistant", "content": "done"}
				reason := "stop"
				if calls != nil {
					message = map[string]any{"role": "assistant", "tool_calls": calls}
					reason = "stop"
				}
				json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "message": message, "done": true, "done_reason": reason, "prompt_eval_count": 100, "eval_count": 5})
			}))
			defer srv.Close()
			cfg := config.Defaults()
			cfg.Model.Server = srv.URL
			cfg.Model.Runtime = "ollama"
			cfg.Model.ToolProtocol = "structured"
			cfg.Auth.Mode = config.AuthAll
			cfg.Recall.TopN = 0
			cfg.Coding.CheckCommand = "true"
			cfg.Behavior.Path = filepath.Join(dir, "behavior")
			os.WriteFile(cfg.Behavior.Path, []byte("ONE_BEHAVIOR_λ"), 0600)
			var approvals []string
			sink := focusDiskLog{dir: filepath.Join(dir, "sessions"), fail: failure}
			bindDecisionFixture(t, &cfg, nil)
			r, _, e := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Session: sink, SessionID: "scope", MaxSteps: 6, Window: 32768, AskYesNo: func(_ context.Context, p string) bool { approvals = append(approvals, p); return true }}, "ONE_INTENT_λ")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = r.Run(context.Background(), "ONE_INTENT_λ")
			a1, _ := plan.PlanApproaches(s, p1.ID)
			a2, _ := plan.PlanApproaches(s, p2.ID)
			if failure != "" {
				if e == nil || len(requests) != 1 || len(a1)+len(a2) != 0 {
					t.Fatalf("record failure continued: requests=%d changes=%d err=%v", len(requests), len(a1)+len(a2), e)
				}
				if r.Continuation.Workflow != nil {
					t.Fatal("unrecorded focus authority committed")
				}
				return
			}
			if e != nil || len(requests) != 5 || len(a1) != 1 || len(a2) != 1 || a1[0].Title != "frozen parent" || a2[0].Title != "next parent" {
				t.Fatalf("frozen batch requests=%d first=%+v second=%+v err=%v", len(requests), a1, a2, e)
			}
			for i, req := range requests {
				base, behavior, intent := 0, 0, 0
				for _, m := range req.Messages {
					c, _ := m["content"].(string)
					if strings.HasPrefix(c, "You are a professional software engineer") {
						base++
					}
					if c == "ONE_BEHAVIOR_λ" {
						behavior++
					}
					if c == "ONE_INTENT_λ" {
						intent++
					}
				}
				if base != 1 || behavior != 1 || intent != 1 {
					t.Errorf("request%d prefix base=%d behavior=%d intent=%d", i+1, base, behavior, intent)
				}
			}
			for _, req := range requests[1:] {
				for _, d := range req.Tools {
					f := d["function"].(map[string]any)
					if f["name"] == "workflow_approach" {
						p := f["parameters"].(map[string]any)["properties"].(map[string]any)
						if p["plan"] != nil {
							t.Fatal("hidden plan leaked into schema")
						}
					}
				}
			}
			if len(approvals) == 0 || !strings.Contains(approvals[0], p1.ID) {
				t.Fatalf("alias focus approval omitted resolved UUID: %v", approvals)
			}
			joined := strings.Join(approvals, "\n")
			if !strings.Contains(joined, p1.ID) || !strings.Contains(joined, p2.ID) {
				t.Fatalf("approval omitted actual targets: %q", joined)
			}
			events, e := session.Read(sink.dir, "scope")
			if e != nil {
				t.Fatal(e)
			}
			receipts := ""
			for _, e := range events {
				if e.Kind == "bound_call" {
					receipts += e.Text + "\n"
				}
			}
			if !strings.Contains(receipts, `"plan":"`+p1.ID+`"`) || !strings.Contains(receipts, `"plan":"`+p2.ID+`"`) {
				t.Fatalf("bound receipts %q", receipts)
			}
			if !strings.Contains(receipts, `workflow_focus {"ref":"`+p1.ID+`"}`) {
				t.Fatalf("focus receipt omitted resolved identity: %s", receipts)
			}
			firstReply := ""
			for _, event := range events {
				if event.Kind == "native_reply" {
					firstReply = event.Text
					break
				}
			}
			var original session.Entry
			if json.Unmarshal([]byte(firstReply), &original) != nil || len(original.Message.ToolCalls) != 1 || !strings.Contains(original.Message.ToolCalls[0].Arguments, `"ref":"P1"`) {
				t.Fatalf("original alias rewritten: %s", firstReply)
			}
			restored, _, e := session.Restore(events, 32768, r.ModelKey)
			if e != nil || restored.Workflow == nil || restored.Workflow.ID != a2[0].ID {
				t.Fatalf("restore %+v %v", restored.Workflow, e)
			}
			copy := restored.Clone()
			copy.Workflow.ID = p1.ID
			if restored.Workflow.ID != a2[0].ID {
				t.Fatal("clone shared scope")
			}
		})
	}
}
