package run

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
	"testing"
)

func TestOrdinaryRunRequiresCheckWithoutEdits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"PASS done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	cfg := config.Defaults()
	cfg.Model.Server = srv.URL
	cfg.Model.ToolProtocol = "text"
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	owned, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer owned.Close()
	bindDecisionFixture(t, &cfg, nil)
	r, _, e := New(Deps{Store: owned, Cfg: cfg, Server: cfg.Model.Server, Model: "fixture", MaxSteps: 1, Window: 32768}, "inspect only")
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = r.Run(context.Background(), "inspect only")
	if e != nil || r.Outcome.Status != outcome.Stopped || r.Outcome.ExitCode() != 1 {
		t.Fatalf("ordinary no-edit final=%+v error=%v", r.Outcome, e)
	}
}
