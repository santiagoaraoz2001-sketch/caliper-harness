package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestOutcomeCLIProcess(t *testing.T) {
	if os.Getenv("HARNESS_OUTCOME_COMPONENT_HELPER") != "1" {
		return
	}
	runAgent(strings.Split(os.Getenv("HARNESS_OUTCOME_COMPONENT_ARGV"), "\x1f"))
}

func TestOutcomeCLICompletedStoppedAndFailed(t *testing.T) {
	for _, tc := range []struct {
		name, reply  string
		status, code int
		want         string
	}{
		{"completed", "actual component final", 200, 0, "actual component final"},
		{"managed stop", "```tool\ntool: absent\nx: y\n```", 200, 1, "run stopped:"},
		{"transport failure", "", 503, 1, "run failed:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			taskID := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if tc.status != 200 {
					http.Error(w, "component transport unavailable", tc.status)
					return
				}
				var request struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if len(request.Messages) > 0 && strings.HasPrefix(request.Messages[len(request.Messages)-1].Content, "Review ") {
					writeTextToolFixture(w, "PASS: observed actual true check")
					return
				}
				reply := tc.reply
				if tc.name == "completed" {
					if requests == 1 {
						reply = "```tool\ntool: task_start\ntask: " + taskID + "\n```"
					}
					if requests == 2 {
						reply = "```tool\ntool: check\ntimeout: 2\n```"
					}
				}
				writeTextToolFixture(w, reply)
			}))
			defer server.Close()
			dir := t.TempDir()
			cfg := config.Defaults()
			cfg.Model.ToolProtocol = "text" // text controls still use streamed tool exchanges
			if tc.name == "completed" {
				cfg.Coding.CheckCommand = "true"
				cfg.Auth.Mode = config.AuthYOLO
			}
			cfg.Model.Server = server.URL + "/v1"
			cfg.Model.Name = "component-only"
			cfg.Store.Path = filepath.Join(dir, "db")
			cfg.Sessions.Path = filepath.Join(dir, "sessions")
			cfg.Behavior.Path = filepath.Join(dir, "absent-behavior")
			cfg.Templates.Path = "../../templates"
			cfg.Recall.TopN = 0
			outcomeDecisionFixture(t, &cfg)
			steps := "2"
			if tc.name == "completed" {
				taskID = outcomeReadyTask(t, cfg)
				steps = "3"
			}
			path := filepath.Join(dir, "config.yaml")
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutcomeCLIProcess$")
			cmd.Env = append(os.Environ(), "HARNESS_OUTCOME_COMPONENT_HELPER=1", "HARNESS_OUTCOME_COMPONENT_ARGV="+strings.Join([]string{"--config", path, "--prompt", "component prompt", "--max-steps", steps}, "\x1f"))
			raw, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.code || !strings.Contains(string(raw), tc.want) {
				t.Fatalf("actual CLI code=%d want=%d output=%s", code, tc.code, raw)
			}
		})
	}
}

func TestOutcomeCLIParentRefusalCannotBeMasked(t *testing.T) {
	for _, mode := range []string{"authorize-all", "yolo"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "db")
			s, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			p, err := plan.CreatePlanContext(plan.WithProject(context.Background(), outcomeProject(t)), s, "component plan", "goal")
			if err != nil {
				t.Fatal(err)
			}
			a, err := plan.CreateApproach(s, p.ID, "draft approach")
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			calls := 0
			taskID := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				reply := "Everything succeeded."
				if calls == 1 {
					reply = "```tool\ntool: task_start\ntask: " + taskID + "\n```"
				}
				if calls == 2 {
					reply = "```tool\ntool: launch\napproach: " + a.ID + "\n```"
				}
				writeTextToolFixture(w, reply)
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.Model.ToolProtocol = "text" // legacy text outcome controls use D13 streaming
			cfg.Model.Server = server.URL + "/v1"
			cfg.Model.Name = "component-only"
			cfg.Store.Path = dbPath
			cfg.Sessions.Path = filepath.Join(dir, "sessions")
			cfg.Behavior.Path = filepath.Join(dir, "absent")
			cfg.Recall.TopN = 0
			cfg.Auth.Mode = mode
			cfg.Coding.CheckCommand = "true"
			cfg.Templates.Path = "../../templates"
			outcomeDecisionFixture(t, &cfg)
			taskID = outcomeReadyTask(t, cfg)
			path := filepath.Join(dir, "config.yaml")
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutcomeCLIProcess$")
			cmd.Env = append(os.Environ(), "HARNESS_OUTCOME_COMPONENT_HELPER=1", "HARNESS_OUTCOME_COMPONENT_ARGV="+strings.Join([]string{"--config", path, "--prompt", "component workflow", "--tools", "full", "--max-steps", "3"}, "\x1f"))
			if mode == "authorize-all" {
				cmd.Stdin = strings.NewReader("y\nn\n")
			} // allow the claim, decline the actual launch
			raw, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || calls != 3 || !strings.Contains(string(raw), "Everything succeeded.") || (!strings.Contains(string(raw), "required workflow unresolved") && !strings.Contains(string(raw), "Model completion claims are unverified")) {
				t.Fatalf("actual refused parent falsely completed: calls=%d err=%v output=%s", calls, err, raw)
			}
		})
	}
}

func writeTextToolFixture(w http.ResponseWriter, reply string) {
	raw, _ := json.Marshal(map[string]any{"model": "component-only", "choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 5}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}
