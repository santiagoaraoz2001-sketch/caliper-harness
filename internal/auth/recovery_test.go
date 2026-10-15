package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

func TestRiskyUnavailableSelectedSidecarRecovery(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	selected := server.URL
	server.Close() // A real unavailable nondefault endpoint, never a model server.
	client := decision.New(selected, "fixture", decision.DeviceCPU, time.Second)
	g, _ := riskyGate(client.Judge)
	var ran int
	res, err := g.Wrap(counting(&ran))(context.Background(), fakeDef(), tool.Args{"cmd": "echo harmless"})
	if err != nil || !res.Blocked || ran != 0 {
		t.Fatalf("blocked action escaped: result=%+v err=%v ran=%d", res, err, ran)
	}
	for _, want := range []string{selected, "decision.server", "decision.runtime"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("recovery omitted %q: %s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "8090") || strings.Contains(res.Text, "python3") || strings.Contains(res.Text, "yolo") {
		t.Fatalf("recovery guessed a different launch or policy: %s", res.Text)
	}
}
