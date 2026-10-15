package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

// Real owning graph and request constructors; model transport only captures the
// request then refuses. No coding, decision inference, or qualification is
// fabricated. The advertised tools distinguish guided versus advanced routes.
func TestEmittedPlanningRoleMatchesActualAssignmentRoutes(t *testing.T) {
	for _, mode := range []string{"ordinary", "guided", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			task := assignedFixtureTask(t, s, "true", "test -d .")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				requests++
				system := ""
				for _, message := range body.Messages {
					if message.Role == "system" {
						system += message.Content + "\n"
					}
				}
				names := map[string]bool{}
				for _, definition := range body.Tools {
					names[definition.Function.Name] = true
				}
				if mode == "ordinary" {
					if !names["write"] || names["task_start"] || !strings.Contains(system, "no PAT is required") || strings.Contains(system, "PLANNING ORCHESTRATOR:") {
						t.Errorf("ordinary tools/role disagree: %v %s", names, system)
					}
				} else {
					if !strings.Contains(system, "PLANNING ORCHESTRATOR:") || !strings.Contains(system, "main model") {
						t.Errorf("selected role forbids an actual advanced route: %s", system)
					}
					if strings.Contains(system, "substitute your own task claim for worker dispatch") {
						t.Errorf("selected role universally forbids the advertised direct claim: %s", system)
					}
					if names["task_start"] != (mode == "advanced") || names["ready_task"] != (mode == "advanced") || !names["write"] || !names["edit"] || !names["bash"] || !names["check"] {
						t.Errorf("actual selected tool route changed: %s %v", mode, names)
					}
					if mode == "guided" && (!names["plan"] || !strings.Contains(system, "answer only plan's current question") || !strings.Contains(system, "pre-claim effects are ordinary project work") || strings.Contains(system, `plan(invoke="EXECUTE")`)) {
						t.Errorf("guided planning lost current-question focus or ordinary draft coding: %v %s", names, system)
					}
				}
				http.Error(w, "component request capture; no generation", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthYOLO, 0, "true"
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Templates.Path = assignedTemplates()
			d := Deps{Tools: "coding", Cfg: cfg, Store: s, Server: server.URL, Model: "component-capture", MaxSteps: 1, Window: 32768}
			if mode != "ordinary" {
				d.Continuation = session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}
			}
			if mode == "advanced" {
				d.Tools = "full"
			}
			runner, _, err := New(d, "inspect the declared work")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = runner.Run(t.Context(), "inspect the declared work")
			if err == nil || requests != 1 {
				t.Fatalf("capture did not stop exactly one real request: requests=%d error=%v", requests, err)
			}
			actual, err := plan.GetTask(s, task.ID)
			if err != nil || actual.State != plan.TaskQueued || actual.Attempts != 0 {
				t.Fatalf("request-only control changed task authority: %+v %v", actual, err)
			}
		})
	}
}
