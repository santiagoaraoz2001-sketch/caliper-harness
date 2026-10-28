package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

func TestActualProviderCheckSchemaCarriesCurrentReportContract(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "opaque", true: "declared"}[declared], func(t *testing.T) {
			t.Chdir(t.TempDir())
			s, err := store.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			captured := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body struct {
					Tools []struct {
						Function struct {
							Name        string
							Description string
						}
					}
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, def := range body.Tools {
					if def.Function.Name == "check" {
						captured <- def.Function.Description
					}
				}
				http.Error(w, "capture only; no inference", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol, cfg.Recall.TopN = "openai", "structured", 0
			cfg.Coding.CheckCommand = "printf fixture"
			if declared {
				cfg.Coding.ReportFormat, cfg.Coding.ExpectedTests = checkreport.StageFormat, []string{"browser-live"}
			}
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: server.URL, Model: "capture", MaxSteps: 1, Window: 32768}, "Inspect the current project")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = r.Run(t.Context(), "Inspect the current project")
			if err == nil || len(captured) != 1 {
				t.Fatalf("capture count/error: %d %v", len(captured), err)
			}
			description := <-captured
			if declared {
				for _, want := range []string{checkreport.StageFormat, "browser-live", "CALIPER_TEST_RUN_ID"} {
					if !strings.Contains(description, want) {
						t.Fatalf("provider schema omits %s: %s", want, description)
					}
				}
			} else if strings.Contains(description, "Configured-suite execution contract") {
				t.Fatal("opaque command advertised undeclared report requirement")
			}
		})
	}
}
