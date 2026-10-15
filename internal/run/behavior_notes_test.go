package run

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

func TestBehaviorBuilderNotesAndSetupRefusal(t *testing.T) {
	dir := t.TempDir()
	c := config.Defaults()
	c.Behavior.Path = filepath.Join(dir, "behavior")
	c.Behavior.MaxTokens = 2
	c.Behavior.CharsPerToken = 2
	c.Recall.TopN = 0
	c.Model.Runtime = "openai"
	if err := os.WriteFile(c.Behavior.Path, []byte("  αβγδεζ  "), 0600); err != nil {
		t.Fatal(err)
	}
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c.Model.Server = srv.URL
	sink := &behaviorLog{}
	bindDecisionFixture(t, &c, nil)
	r, notes, e := New(Deps{Cfg: c, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 1, Store: s, Session: sink, In: strings.NewReader(""), Out: io.Discard}, "task")
	if e != nil {
		t.Fatal(e)
	}
	note := "note: behavior file (10 chars) exceeds ~2 tokens — keeping the last 4 chars"
	if r.Behavior != "γδεζ" || r.BehaviorNote != note || !strings.Contains(strings.Join(notes, "\n"), note) {
		t.Fatalf("parent shape/notice %q %q %v", r.Behavior, r.BehaviorNote, notes)
	}
	if _, _, e = r.Run(context.Background(), "task"); e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, ev := range sink.events {
		if ev.Kind == "note" && ev.Text == note {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("durable note count %d", count)
	}
	d, e := LaunchDeps(c, s, srv.URL, "fixture", 1, 32768, strings.NewReader(""), io.Discard)
	if e != nil || d.Behavior != r.Behavior || d.BehaviorNote != note {
		t.Fatalf("headless shape/notice %+v %v", d, e)
	}
	if err := os.WriteFile(c.Behavior.Path, []byte("NEW"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh, e := LaunchDeps(c, s, srv.URL, "fixture", 1, 32768, nil, io.Discard)
	if e != nil || fresh.Behavior != "NEW" || fresh.BehaviorNote != "" || d.Behavior != "γδεζ" {
		t.Fatal("builder snapshot/reload", e)
	}
	before := requests.Load()
	c.Behavior.Path = dir // directories are reliably unreadable as files, including root.
	if _, _, e := New(Deps{Cfg: c, Store: s, Server: srv.URL, Model: "fixture"}, "task"); e == nil || !strings.Contains(e.Error(), "cannot read behavior file") {
		t.Fatalf("parent unreadable %v", e)
	}
	if _, e := LaunchDeps(c, s, srv.URL, "fixture", 1, 32768, nil, io.Discard); e == nil || !strings.Contains(e.Error(), "cannot read behavior file") {
		t.Fatalf("headless unreadable %v", e)
	}
	if requests.Load() != before {
		t.Fatal("setup failure forwarded")
	}
	c.Behavior.Path = filepath.Join(dir, "missing")
	missing, e := LaunchDeps(c, s, srv.URL, "fixture", 1, 32768, nil, io.Discard)
	if e != nil || missing.Behavior != "" || missing.BehaviorNote != "" {
		t.Fatal("missing semantics", e)
	}
}
