package serve

import (
	"context"
	"testing"
)

func TestSameEndpoint(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"http://localhost:8080", "http://127.0.0.1:8080/v1", true},
		{"http://remote:8080", "http://127.0.0.1:8080", false},
		{"http://[::1]:8080", "http://127.0.0.1:8080", false},
		{"http://localhost", "http://127.0.0.1:80/", true},
		{"https://localhost", "http://localhost:443", false},
		{"http://localhost/a/v1", "http://localhost/b/v1", false},
		{"http://localhost/a/v1/", "http://localhost/a", true},
		{"bad", "bad", false},
	} {
		if got := SameEndpoint(tc.a, tc.b); got != tc.same {
			t.Errorf("SameEndpoint(%q,%q)=%v want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestDiscoveryRequiresPositiveProtocolIdentity(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{{500, "busy"}, {200, `{}`}} {
		c := client(map[string]struct {
			status int
			body   string
		}{"/api/ps": route(response.status, response.body)})
		kind, _, ok := fingerprint(context.Background(), c, "http://127.0.0.1:9876")
		if ok {
			t.Fatalf("unrelated service misidentified as %s", kind)
		}
	}
	c := client(map[string]struct {
		status int
		body   string
	}{"/api/ps": route(500, "unrelated route"), "/v1/models": route(200, `{"data":[{"id":"worker"}]}`)})
	kind, _, ok := fingerprint(context.Background(), c, "http://127.0.0.1:9876")
	if !ok || kind != KindOpenAI {
		t.Fatalf("OpenAI discovery obscured by unrelated endpoint: %s %v", kind, ok)
	}
}
