package run

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestAuthorizationListsAtOwningFactory(t *testing.T) {
	t.Chdir(t.TempDir())
	owned, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected decision or model request", 503)
	}))
	defer srv.Close()
	base := config.Defaults()
	base.Model.Server = srv.URL
	base.Decision.Server = srv.URL
	base.Recall.TopN = 0
	build := func(c config.Config) error {
		_, _, e := New(Deps{Store: owned, Cfg: c, Server: srv.URL, Model: "fixture", Out: io.Discard, ReplyOut: io.Discard}, "owned fixture")
		if e == nil {
			_, e = LaunchDeps(c, owned, srv.URL, "fixture", 3, 32768, strings.NewReader(""), io.Discard)
		}
		return e
	}
	for _, mode := range []string{config.AuthRisky, config.AuthAll, config.AuthYOLO} {
		for _, tc := range []struct {
			name         string
			free, judged []string
			key, word    string
		}{
			{"blank", []string{""}, nil, "auth.free_tools", ""},
			{"duplicate", []string{"grep", "grep"}, nil, "auth.free_tools", "grep"},
			{"overlap", []string{"grep"}, []string{"grep"}, "auth.judged_tools", "grep"},
			{"unknown", []string{"absent_tool"}, nil, "auth.free_tools", "absent_tool"},
			{"unknown judged", nil, []string{"absent_tool"}, "auth.judged_tools", "absent_tool"},
			{"legacy", []string{"search"}, nil, "auth.free_tools", "grep"},
			{"noncanonical", []string{" BASH "}, nil, "auth.free_tools", "BASH"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				c := base
				c.Auth.Mode = mode
				c.Auth.FreeTools = tc.free
				c.Auth.JudgedTools = tc.judged
				e := build(c)
				_, directErr := LaunchDeps(c, owned, srv.URL, "fixture", 3, 32768, strings.NewReader(""), io.Discard)
				if directErr == nil || !strings.Contains(directErr.Error(), tc.key) {
					t.Fatalf("direct launch must refuse before child creation: %v", directErr)
				}
				if e == nil || !strings.Contains(e.Error(), tc.key) || !strings.Contains(e.Error(), tc.word) {
					t.Fatalf("expected actionable preflight refusal: %v", e)
				}
			})
		}
	}
	c := base
	c.Auth.FreeTools = nil
	c.Auth.JudgedTools = []string{"check", "launch", "judge", "recall", "workflow_task"}
	if e := build(c); e != nil {
		t.Fatalf("complete owning names must survive coding filtering: %v", e)
	}
	ld, e := LaunchDeps(base, owned, srv.URL, "fixture", 3, 32768, strings.NewReader(""), io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	child := ld.NewRegistry()
	for _, name := range []string{"judge", "recall", "launch", "workflow_task", "check"} {
		if _, ok := child.Lookup(name); ok {
			t.Fatalf("admission leaked parent-only tool %s into child", name)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("preflight sent %d requests", requests.Load())
	}
}

func TestDefaultFreeSearchUsesActualRegistry(t *testing.T) {
	t.Chdir(t.TempDir())
	if e := os.WriteFile("owned.txt", []byte("needle\n"), 0600); e != nil {
		t.Fatal(e)
	}
	owned, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected judged free search", 503)
	}))
	defer srv.Close()
	c := config.Defaults()
	c.Auth.Mode = config.AuthRisky
	c.Model.Server = srv.URL
	c.Decision.Server = srv.URL
	c.Recall.TopN = 0
	r, _, e := New(Deps{Store: owned, Cfg: c, Server: srv.URL, Model: "fixture", Out: io.Discard, ReplyOut: io.Discard}, "owned")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ name, raw string }{{"grep", `{"pattern":"needle","path":"owned.txt"}`}, {"find_file", `{"pattern":"owned.txt"}`}} {
		d, ok := r.Registry.Lookup(tc.name)
		if !ok {
			t.Fatal(tc.name)
		}
		a, repair := tool.ValidateJSON(d, tc.raw)
		if repair != "" {
			t.Fatal(repair)
		}
		result, e := r.Exec(context.Background(), d, a)
		if e != nil || result.Blocked || result.Failed || !strings.Contains(result.Text, "owned.txt") {
			t.Fatalf("free %s result=%+v error=%v", tc.name, result, e)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("free search made %d decision requests", requests.Load())
	}
}
