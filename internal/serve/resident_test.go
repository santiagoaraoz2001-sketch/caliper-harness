package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestD09ResidentSelectedServerNoPull(t *testing.T) {
	var resident atomic.Bool
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"installed:exact"}]}`))
		case "/api/ps":
			if resident.Load() {
				w.Write([]byte(`{"models":[{"name":"installed:exact"}]}`))
			} else {
				w.Write([]byte(`{"models":[]}`))
			}
		case "/api/generate":
			posts.Add(1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "installed:exact" || body["prompt"] != "" || body["stream"] != false {
				t.Errorf("unexpected body %#v", body)
			}
			resident.Store(body["keep_alive"] != float64(0))
			w.Write([]byte(`{"model":"installed:exact","done":true}`))
		default:
			t.Errorf("unexpected route (no pull allowed) %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	sentinel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unselected endpoint contacted") }))
	defer sentinel.Close()
	for _, load := range []bool{true, false} {
		h, err := SetOllamaResident(context.Background(), srv.Client(), srv.URL+"/v1", "installed:exact", load)
		if err != nil || !h.Up || resident.Load() != load {
			t.Fatalf("load %t %+v %v", load, h, err)
		}
	}
	if _, err := SetOllamaResident(context.Background(), srv.Client(), srv.URL, "not-installed", true); err == nil {
		t.Fatal("unknown model accepted")
	}
	if posts.Load() != 2 {
		t.Fatalf("unexpected posts %d", posts.Load())
	}
}
func TestD09ResidentNeedsReadbackAndCancellation(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "notresident", true: "cancelreadback"}[cancelRead], func(t *testing.T) {
			read := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/tags":
					w.Write([]byte(`{"models":[{"name":"m"}]}`))
				case "/api/generate":
					w.Write([]byte(`{"model":"m","done":true}`))
				case "/api/ps":
					if cancelRead {
						close(read)
						<-release
					}
					w.Write([]byte(`{"models":[]}`))
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := SetOllamaResident(ctx, srv.Client(), srv.URL, "m", true); done <- err }()
			if cancelRead {
				<-read
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || (cancelRead && !strings.Contains(err.Error(), "context canceled")) {
					t.Fatalf("not refused %v", err)
				}
			case <-time.After(time.Second):
				close(release)
				t.Fatal("readback ignored cancellation")
			}
			close(release)
		})
	}
}

func TestD09ResidentRejectsExplicitNativeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags", "/api/ps":
			w.Write([]byte(`{"models":[{"name":"m"}]}`))
		case "/api/generate":
			w.Write([]byte(`{"model":"m","done":true,"error":"controlled load failure"}`))
		}
	}))
	defer srv.Close()
	if _, err := SetOllamaResident(context.Background(), srv.Client(), srv.URL, "m", true); err == nil || !strings.Contains(err.Error(), "controlled load failure") {
		t.Fatalf("explicit native error hidden %v", err)
	}
}
