package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

type cancellationOutput struct {
	buffer   bytes.Buffer
	once     sync.Once
	prompted chan struct{}
}

func (w *cancellationOutput) String() string { return w.buffer.String() }

func (w *cancellationOutput) Write(b []byte) (int, error) {
	n, e := w.buffer.Write(b)
	if bytes.Contains(w.buffer.Bytes(), []byte("answer:")) {
		w.once.Do(func() { close(w.prompted) })
	}
	return n, e
}

func TestCancellationCLIExitAndOwnedInput(t *testing.T) {
	for _, kind := range []string{"bash", "ask"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			started, late := filepath.Join(dir, "started"), filepath.Join(dir, "late")
			reply := "```tool\ntool: ask\nquestion: Wait for input\n```"
			if kind == "bash" {
				reply = "```tool\ntool: bash\ncmd: printf RETAINED; printf START > '" + started + "'; sleep 0.5; printf LATE > '" + late + "'\n```"
			}
			taskID, calls := "", 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				text := reply
				if taskID != "" && calls == 1 {
					text = "```tool\ntool: task_start\ntask: " + taskID + "\n```"
				}
				writeTextToolFixture(w, text)
			}))
			defer srv.Close()
			cfg := config.Defaults()
			cfg.Model.ToolProtocol = "text" // original text cancellation, streamed ingestion
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Model.Server, cfg.Model.Name = srv.URL+"/v1", "scripted-component"
			cfg.Store.Path, cfg.Sessions.Path = filepath.Join(dir, "db"), filepath.Join(dir, "sessions")
			cfg.Behavior.Path, cfg.Templates.Path, cfg.Recall.TopN = filepath.Join(dir, "absent"), "../../templates", 0
			outcomeDecisionFixture(t, &cfg)
			steps := "3"
			if kind == "bash" {
				cfg.Coding.CheckCommand = "true"
				taskID = outcomeReadyTask(t, cfg)
				steps = "4" // the actual task_start consumes one additional step
			}
			path := filepath.Join(dir, "config.yaml")
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOutcomeCLIProcess$")
			prepareCancellationProcess(cmd)
			cmd.Env = append(os.Environ(), "HARNESS_OUTCOME_COMPONENT_HELPER=1", "HARNESS_OUTCOME_COMPONENT_ARGV="+strings.Join([]string{"--config", path, "--prompt", "cancellation component", "--max-steps", steps}, "\x1f"))
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			out := &cancellationOutput{prompted: make(chan struct{})}
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					cancel()
					_ = cmd.Wait()
					t.Log(out.String())
				}
			}()
			if kind == "ask" {
				select {
				case <-out.prompted:
				case <-time.After(3 * time.Second):
					t.Fatal("CLI Ask did not prompt")
				}
			} else {
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, err := os.Stat(started); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("CLI Bash did not start")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err := interruptCancellationProcess(cmd); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 130 || !strings.Contains(out.String(), "run cancelled:") {
				t.Fatalf("exit/output lost cancellation: %v %s", err, out.String())
			}
			if kind == "bash" {
				ids, err := session.List(cfg.Sessions.Path)
				if err != nil || len(ids) != 1 {
					t.Fatalf("session missing: %v %v", ids, err)
				}
				events, err := session.Read(cfg.Sessions.Path, ids[0])
				if err != nil {
					t.Fatal(err)
				}
				retained := false
				for _, e := range events {
					if e.Kind == "result" && strings.Contains(e.Text, "partial tool result (failed; not complete):\nRETAINED") {
						retained = true
					}
				}
				if !retained {
					t.Fatalf("actual partial result absent: %+v", events)
				}
			}
			time.Sleep(550 * time.Millisecond)
			if _, err := os.Stat(late); err == nil {
				t.Fatal("CLI child wrote after cancellation")
			}
			s, err := store.Open(cfg.Store.Path)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
		})
	}
}
