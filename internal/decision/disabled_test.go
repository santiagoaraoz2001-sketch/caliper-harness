package decision

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type forbiddenDecisionTransport struct{ t *testing.T }

func (f forbiddenDecisionTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("disabled judge contacted server")
	return nil, errors.New("forbidden")
}
func TestDisabledDecisionNeverContactsServer(t *testing.T) {
	c := New("http://127.0.0.1:8090", "preserved-model", "gpu", time.Second).WithDisabled(true)
	c.http.Transport = forbiddenDecisionTransport{t}
	if _, err := c.Judge(context.Background(), Request{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("judge: %v", err)
	}
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("health: %v", err)
	}
	if _, err := c.Activate(context.Background(), Selection{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("activate: %v", err)
	}
	if c.model != "preserved-model" || c.device != "gpu" {
		t.Fatal("selection lost")
	}
}
