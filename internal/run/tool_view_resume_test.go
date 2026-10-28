package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"testing"
)

func TestResumedToolViewActualNativeRequest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	bindDecisionFixture(t, &cfg, nil)
	for _, tc := range []struct {
		name, saved, explicit string
		full                  bool
	}{{"full", "full", "", true}, {"coding", "coding", "", false}, {"legacy", "", "", false}, {"explicit override", "full", "coding", false}} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				fetch, read := false, false
				for _, d := range body.Tools {
					fetch = fetch || d.Function.Name == "fetch"
					read = read || d.Function.Name == "read"
				}
				if fetch != tc.full || !read {
					t.Errorf("actual resumed tools full=%v/read=%v want full=%v", fetch, read, tc.full)
				}
				requests++
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Inspection complete.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			// Persist the exact shape used by the session browser/CLI resume, then use New.
			c := session.Continuation{Version: 1, ToolView: tc.saved}
			c.Add("intent", "user", "Earlier inspection", false)
			raw, err := c.Snapshot().Encode()
			if err != nil {
				t.Fatal(err)
			}
			if err = session.Append(filepath.Join(dir, "saved"), tc.name, session.Event{Kind: "continuation", Text: raw}); err != nil {
				t.Fatal(err)
			}
			events, err := session.Read(filepath.Join(dir, "saved"), tc.name)
			if err != nil {
				t.Fatal(err)
			}
			c, _, err = session.Restore(events, 32768)
			if err != nil {
				t.Fatal(err)
			}
			r, _, err := New(Deps{Tools: tc.explicit, Cfg: cfg, Store: s, Server: srv.URL, Model: "resume-control", Session: &codingSink{dir: filepath.Join(dir, "next")}, SessionID: tc.name, Continuation: c, Window: 65536, MaxSteps: 1}, "Inspect only")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = r.Run(context.Background(), "Inspect only"); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || r.Continuation.Assignment != nil {
				t.Fatalf("resume changed execution ownership requests=%d", requests)
			}
			want := tc.saved
			if tc.explicit != "" {
				want = tc.explicit
			}
			if r.Continuation.ToolView != want {
				t.Fatalf("preference lost: %q want%q", r.Continuation.ToolView, want)
			}
		})
	}
	c := session.Continuation{Version: 1, ToolView: "reviewer"}
	c.Add("intent", "user", "bad", false)
	if c.Validate() == nil {
		t.Fatal("transient role accepted as user tool preference")
	}
}

func TestResumedWorkflowExposureRequiresExplicitChoice(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	t.Chdir(f.d.Workspace)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 0
	bindDecisionFixture(t, &cfg, nil)
	for _, tc := range []struct {
		name, saved, explicit string
		exposed               bool
	}{
		{"legacy selected metadata", "", "", false},
		{"saved coding selected metadata", "coding", "", false},
		{"explicit coding selected metadata", "", "coding", false},
		{"saved full selected metadata", "full", "", true},
		{"explicit full selected metadata", "", "full", true},
		{"saved explicit workflow", "workflow", "", true},
		{"explicit workflow", "", "workflow", true},
		{"explicit coding overrides workflow", "workflow", "coding", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := session.Continuation{Version: 1, ToolView: tc.saved, Workflow: f.scope}
			c.Add("intent", "user", "Saved user request", false)
			raw, err := c.Snapshot().Encode()
			if err != nil {
				t.Fatal(err)
			}
			restored, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 65536)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				prepare := false
				for _, def := range body.Tools {
					prepare = prepare || def.Function.Name == "prepare_task"
				}
				if prepare != tc.exposed {
					t.Errorf("selected metadata broadened actual request: prepare_task=%v want%v", prepare, tc.exposed)
				}
				requests++
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"inspect only\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			r, _, err := New(Deps{Tools: tc.explicit, Cfg: cfg, Store: f.d.Store, Server: srv.URL, Model: "scope-resume", Continuation: restored, Session: &codingSink{dir: t.TempDir()}, SessionID: "scope-resume", Window: 65536, MaxSteps: 1}, "Inspect selected work only")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = r.Run(t.Context(), "Inspect selected work only"); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || r.Continuation.Assignment != nil || r.Continuation.Workflow == nil || *r.Continuation.Workflow != *f.scope {
				t.Fatalf("scope/ownership changed: %+v", r.Continuation)
			}
			want := tc.saved
			if tc.explicit != "" {
				want = tc.explicit
			}
			if r.Continuation.ToolView != want {
				t.Fatalf("saved explicit preference %q want%q", r.Continuation.ToolView, want)
			}
		})
	}
	for _, explicit := range []string{"", "workflow"} {
		c := session.Continuation{Version: 1, ToolView: "workflow"}
		c.Add("intent", "user", "bad", false)
		if c.Validate() == nil {
			t.Error("workflow exposure without scope validates")
		}
		if _, _, err := New(Deps{Tools: explicit, Cfg: cfg, Store: f.d.Store, Continuation: c, Window: 65536}, "inspect"); err == nil {
			t.Error("workflow exposure without scope creates runner")
		}
	}
}

func TestRestoredExplicitWorkflowRejectsForeignOwnership(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	t.Chdir(f.d.Workspace)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	foreign, err := plan.CreatePlanContext(plan.WithProject(t.Context(), t.TempDir()), f.d.Store, "other", "other project")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []*session.WorkflowScope{{Store: "foreign-store", Kind: "task", ID: f.scope.ID}, {Store: f.d.Store.Identity(), Kind: "plan", ID: foreign.ID}} {
		c := session.Continuation{Version: 1, ToolView: "workflow", Workflow: scope}
		c.Add("intent", "user", "preserved", false)
		raw, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		c, _, err = session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 65536)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = New(Deps{Cfg: cfg, Store: f.d.Store, Continuation: c, Window: 65536}, "inspect"); err == nil {
			t.Fatal("foreign workflow owner prepared a runner")
		}
	}
}
