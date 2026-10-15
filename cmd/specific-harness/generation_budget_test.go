package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/config"
	"strings"
	"testing"
)

func TestGenerationBudgetCLIHelper(t *testing.T) {
	if os.Getenv("HARNESS_CAP_COMPONENT_HELPER") != "1" {
		return
	}
	runChat(strings.Split(os.Getenv("HARNESS_CAP_COMPONENT_ARGV"), "\x1f"))
}
func TestGenerationBudgetDirectCLIFactories(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "SSE"}[stream], func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				calls++
				if b["max_completion_tokens"] != float64(16384) {
					t.Errorf("direct CLI budget missing: %v", b)
				}
				if stream {
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"CAP_COMPONENT\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"CAP_COMPONENT"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()
			c := config.Defaults()
			c.Model.Server = srv.URL
			c.Model.Name = "fixture"
			p := filepath.Join(t.TempDir(), "cap.yaml")
			if err := config.Save(p, c); err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", p, "--prompt", "task"}
			if !stream {
				args = append(args, "--no-stream")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestGenerationBudgetCLIHelper$")
			cmd.Env = append(os.Environ(), "HARNESS_CAP_COMPONENT_HELPER=1", "HARNESS_CAP_COMPONENT_ARGV="+strings.Join(args, "\x1f"))
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "CAP_COMPONENT") || calls != 1 {
				t.Fatalf("actual direct CLI failed calls=%d err=%v out=%s", calls, err, out)
			}
		})
	}
}
