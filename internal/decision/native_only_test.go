package decision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUnsupportedJudgeRuntimeNeverContactsServer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; t.Errorf("unexpected request %s", r.URL) }))
	defer server.Close()
	for _, runtime := range []string{"openai", "mlx", "ollama", "chat"} {
		selection := Selection{Model: "ordinary", Device: DeviceAuto, Runtime: runtime}
		c := New(server.URL, selection.Model, selection.Device, time.Second).WithSelection(selection)
		if _, err := c.Judge(context.Background(), Request{Context: "test", Options: []string{"yes", "no"}}); err == nil {
			t.Errorf("Judge accepted %s", runtime)
		}
		if _, err := c.Health(context.Background()); err == nil {
			t.Errorf("Health accepted %s", runtime)
		}
		if _, err := c.Activate(context.Background(), selection); err == nil {
			t.Errorf("Activate accepted %s", runtime)
		}
	}
	if requests != 0 {
		t.Fatalf("sent %d unsupported requests", requests)
	}
}
