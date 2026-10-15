package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"specific-harness/internal/config"
	"strings"
	"testing"
)

func TestFetchRefusesBrokenContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "partial")
	}))
	defer server.Close()
	f := &Fetcher{cfg: config.FetchConfig{MaxBytes: 1024}, client: server.Client()}
	res, err := f.Fetch(context.Background(), server.URL+"/broken")
	if err == nil {
		t.Fatalf("interrupted body accepted as success: %+v", res)
	}
	if !strings.Contains(err.Error(), server.URL+"/broken") || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("URL/cause missing: %v", err)
	}
	if res.Text != "partial" || !res.Failed {
		t.Errorf("partial failed evidence lost: %+v", res)
	}
}

func TestFetchLegitimateEmptyBody(t *testing.T) {
	f := testFetcher(200, "text/plain", "")
	res, err := f.Fetch(context.Background(), "https://fixture.invalid/empty")
	if err != nil || res.Text != "" || res.Failed || res.Truncated {
		t.Fatalf("legitimate empty page refused: %+v %v", res, err)
	}
}
