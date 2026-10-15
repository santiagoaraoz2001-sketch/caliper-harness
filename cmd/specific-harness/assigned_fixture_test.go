package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"testing"
)

// Only decision responses are doubles; the subprocess claims an actual ready
// same-project Task and executes its original registered tools and receipt path.
func outcomeDecisionFixture(t *testing.T, cfg *config.Config) {
	t.Helper()
	cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
	selection := decision.SelectionFor(cfg.Decision)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: selection}, "backends": []string{"cpu"}})
		case "/judge":
			var request decision.Request
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": request.Options[0], "confidence": 1, "model": "component", "device": "cpu"})
		default:
			t.Errorf("unbound decision route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	cfg.Decision.Server = srv.URL
	t.Cleanup(srv.Close)
}
func outcomeProject(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func outcomeReadyTask(t *testing.T, cfg config.Config) string {
	t.Helper()
	s, err := store.Open(cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := plan.WithProject(context.Background(), outcomeProject(t))
	p, err := plan.CreatePlanContext(ctx, s, "CLI owning fixture", "perform the actual declared operation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "one strategy")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "actual operation", "perform the requested actual operation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(s, task.ID, "initial", "declared operation complete", "registered tools", "actual check exit0", "declared command", "independently inspect the actual check", "true", "printf CLI_FIXTURE_ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Ready(ctx, s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return task.ID
}
