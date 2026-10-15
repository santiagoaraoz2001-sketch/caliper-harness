package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

// exampleComHTML is byte-exact output captured 2026-10-06 via curl
// (IANA reserved page, stable for years). Embedded so no test hits network.
const exampleComHTML = `<!doctype html><html lang=en><head><meta charset=utf-8><link rel=icon href=data:,><meta name=viewport content="width=device-width,initial-scale=1"><title>Example Domain</title><style>html{color-scheme:light dark;background:light-dark(#eee,#222)}body{font:16px/1.6 system-ui,sans-serif;max-width:26em;margin:auto;padding:25vh 2em 2em;text-align:center}</style></head><body><p>This domain is for use in documentation examples without needing permission. This is not a service; avoid relying on it for testing and monitoring purposes.</p><script src=/s.js></script></body></html>
`

type stubTransport struct {
	status int
	ctype  string
	body   string
}

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", s.status, http.StatusText(s.status)),
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{s.ctype}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    r,
	}, nil
}

func testFetcher(status int, ctype, body string) *Fetcher {
	f := &Fetcher{cfg: config.FetchConfig{TimeoutSeconds: 5, MaxBytes: 262144}}
	f.client = &http.Client{Transport: &stubTransport{status: status, ctype: ctype, body: body}}
	return f
}

func fetchURL(t *testing.T, f *Fetcher, url string) (tool.Result, error) {
	t.Helper()
	d := New(config.FetchConfig{TimeoutSeconds: 5, MaxBytes: 262144})
	args, repair := tool.Validate(d, map[string]string{"url": url})
	if repair != "" {
		t.Fatalf("unexpected repair: %s", repair)
	}
	_ = args
	return f.Fetch(context.Background(), url)
}

func TestRealCapturedPage(t *testing.T) {
	f := testFetcher(200, "text/html", exampleComHTML)
	res, err := fetchURL(t, f, "https://example.com")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	for _, want := range []string{"# Example Domain", "documentation examples", "not a service"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("missing %q in:\n%s", want, res.Text)
		}
	}
	for _, banned := range []string{"color-scheme", "/s.js", "<p>", "system-ui"} {
		if strings.Contains(res.Text, banned) {
			t.Errorf("leaked %q in:\n%s", banned, res.Text)
		}
	}
	if res.Truncated {
		t.Error("small page must not truncate")
	}
}

func TestCodeBlocksFencedVerbatim(t *testing.T) {
	page := `<html><body><p>Run:</p><pre>if x  >  1:
    launch()</pre></body></html>`
	f := testFetcher(200, "text/html", page)
	res, err := fetchURL(t, f, "https://x.test/code")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !strings.Contains(res.Text, "```\nif x  >  1:\n    launch()\n```") {
		t.Errorf("code not fenced verbatim:\n%s", res.Text)
	}
}

func TestLinksKept(t *testing.T) {
	page := `<html><body><a href="https://go.dev">Go site</a></body></html>`
	f := testFetcher(200, "text/html", page)
	res, err := fetchURL(t, f, "https://x.test/l")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !strings.Contains(res.Text, "Go site (https://go.dev)") {
		t.Errorf("text=%q", res.Text)
	}
}

func TestJSONPassesThrough(t *testing.T) {
	f := testFetcher(200, "application/json", `{"a":1}`)
	res, err := fetchURL(t, f, "https://x.test/j")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if strings.TrimSpace(res.Text) != `{"a":1}` {
		t.Errorf("text=%q", res.Text)
	}
}

func TestStatusErrorNamesPage(t *testing.T) {
	f := testFetcher(404, "text/html", "gone")
	_, err := fetchURL(t, f, "https://x.test/missing")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "x.test/missing") {
		t.Errorf("error=%v", err)
	}
}

func TestBinaryRefused(t *testing.T) {
	f := testFetcher(200, "image/png", "\x89PNG...")
	_, err := fetchURL(t, f, "https://x.test/i.png")
	if err == nil || !strings.Contains(err.Error(), "not text") {
		t.Errorf("error=%v", err)
	}
}

func TestOverCapTruncates(t *testing.T) {
	f := testFetcher(200, "text/plain", strings.Repeat("w", 300000))
	f.cfg.MaxBytes = 1024
	res, err := fetchURL(t, f, "https://x.test/big")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !res.Truncated || !strings.Contains(res.Text, "[page truncated:") {
		t.Errorf("truncated=%v", res.Truncated)
	}
}

func TestBadURLFailsPlainly(t *testing.T) {
	f := testFetcher(200, "text/html", "x")
	_, err := fetchURL(t, f, "not a url")
	if err == nil || !strings.Contains(err.Error(), "not a fetchable URL") {
		t.Errorf("error=%v", err)
	}
}
