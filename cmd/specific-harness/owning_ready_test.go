package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"sync/atomic"
	"testing"
)

// Actual public main/graph processes and persisted Kuzu, with an admitted
// deterministic HTTP judge fixture. No native model or sidecar is started.
func TestOwningCLIReady(t *testing.T) {
	for _, mode := range []string{"allow", "refuse", "overflow", "missing check"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "db")
			s, err := store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := plan.CreatePlan(s, "p", "goal")
			a, _ := plan.CreateApproach(s, p.ID, "a")
			x, _ := plan.CreateTask(s, a.ID, "task", "prompt")
			command := "true"
			if mode == "missing check" {
				command = ""
			}
			plan.SetTaskBlock(s, x.ID, "current", "target", "sketch", "exit code 0", "tests", "LATE_VERIFY", command, "printf CLI_FIXTURE_ACCEPTANCE")
			s.Close()
			cfg := config.Defaults()
			cfg.Decision.Model = "component"
			cfg.Decision.Device = "cpu"
			cfg.Decision.Runtime = "laya"
			cfg.Decision.CoherenceMaxChars = 0
			cfg.Model.Server = "http://127.0.0.1:1/v1"
			cfg.Model.Name = "unused-component"
			sel := decision.SelectionFor(cfg.Decision)
			var judges atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/health":
					json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: sel}, "backends": []string{"cpu"}})
				case "/judge":
					judges.Add(1)
					var req struct {
						Context string
						Options []string
					}
					json.NewDecoder(r.Body).Decode(&req)
					if !strings.Contains(req.Context, "LATE_VERIFY") {
						t.Error("late brief missing")
					}
					pick := 0
					if mode == "refuse" {
						pick = 1
					}
					json.NewEncoder(w).Encode(map[string]any{"pick": pick, "option": req.Options[pick], "confidence": 1, "model": "component", "device": "cpu"})
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			cfg.Decision.Server = srv.URL
			if mode == "overflow" {
				cfg.Decision.CoherenceMaxChars = 1
			}
			cfgPath := filepath.Join(dir, "config.yaml")
			if err := config.Save(cfgPath, cfg); err != nil {
				t.Fatal(err)
			}
			argv := []string{"graph", "--config", cfgPath, "--db", db, "--sessions-dir", filepath.Join(dir, "sessions"), "--query", fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id state}}`, a.ID)}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HARNESS_CLI_PROCESS=1", "HARNESS_CLI_ARGV="+strings.Join(argv, "\x1f"))
			cmd.Stdin = strings.NewReader("")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err = cmd.Run()
			if (err == nil) != (mode == "allow" || mode == "overflow") {
				t.Fatalf("exit %v output %s", err, out.String())
			}
			if mode == "missing check" && judges.Load() != 0 {
				t.Fatal("oversize judged")
			}
			if mode != "missing check" && judges.Load() != 1 {
				t.Fatal(judges.Load())
			}
			s, err = store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, _ := plan.GetApproach(s, a.ID)
			task, _ := plan.GetTask(s, x.ID)
			want := plan.ApproachDraft
			if mode == "allow" || mode == "overflow" {
				want = plan.ApproachReady
			}
			if got.State != want || task.Attempts != 0 || task.Verify != "LATE_VERIFY" {
				t.Fatalf("readback %+v %+v", got, task)
			}
		})
	}
}
