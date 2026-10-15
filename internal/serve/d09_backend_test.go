package serve

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestD09ExactBaseAnd404Alternate(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "404-only"}[alternate], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (!alternate && r.URL.Path == "/proxy/v1/models") || (alternate && r.URL.Path == "/proxy/models") {
					w.Write([]byte(`{"data":[]}`))
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			h := Probe(srv.Client(), Endpoint{Kind: KindOpenAI, URL: srv.URL + "/proxy/v1"})
			if !h.Up {
				t.Fatalf("exact API base/404 alternate rejected %+v", h)
			}
		})
	}
}
func TestD09CompleteModelInventoryShape(t *testing.T) {
	for _, kind := range []string{KindOpenAI, KindOllama} {
		for _, body := range []string{`{}`, `{"data":null,"models":null}`, `{"data":[],"models":[]} trailing`, `{"data":[{"id":""}],"models":[{"name":""}]}`} {
			t.Run(kind+body, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
				defer srv.Close()
				h := Probe(srv.Client(), Endpoint{Kind: kind, URL: srv.URL})
				if h.Up {
					t.Fatalf("garbage inventory claimed ready %+v", h)
				}
			})
		}
	}
}
func TestD09OpenAIDialFailureDoesNotTryOtherRoute(t *testing.T) {
	var calls atomic.Int32
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &url.Error{Op: "GET", URL: r.URL.String(), Err: errors.New("owned simulated unreachable")}
	})}
	h := Probe(c, Endpoint{Kind: KindOpenAI, URL: "http://fixture.invalid/v1"})
	if h.Up || !strings.Contains(h.Err, "unreachable") || calls.Load() != 1 {
		t.Fatalf("non404 tried fallback calls%d %+v", calls.Load(), h)
	}
}
