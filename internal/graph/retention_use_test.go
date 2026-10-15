package graph

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"testing"
	"time"
)

func TestR25GraphExplicitUse(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{`{ session(id:"used") { id } }`, `{ session(id:"used") { id events { kind text } } }`} {
		t.Run(q, func(t *testing.T) {
			dir := t.TempDir()
			old := time.Now().AddDate(0, -4, 0)
			for _, id := range []string{"used", "unused"} {
				if err := session.Append(dir, id, session.Event{Kind: "prompt", Text: "raw α"}); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(filepath.Join(dir, id+".jsonl"), old, old); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, "used.jsonl")
			before, _ := os.ReadFile(p)
			api := fixtureGraph(s, "test", dir)
			if _, err := api.ExecBounded(context.Background(), `{ sessions { id events { kind text } } }`, "", nil, config.Defaults().Tools.GraphQL); err != nil {
				t.Fatal(err)
			}
			st, _ := os.Stat(p)
			if !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
				t.Fatal("aggregate refreshed last use")
			}
			if _, err := api.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("use changed raw bytes")
			}
			deleted, err := session.Sweep(dir, 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(deleted) != 1 || deleted[0] != "unused" {
				t.Errorf("named read did not retain used session: %v", deleted)
			}
		})
	}
}

func TestR25GraphUseErrors(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.jsonl")
	old := time.Now().AddDate(0, -4, 0)
	if err := os.WriteFile(p, []byte("bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	api := fixtureGraph(s, "test", dir)
	if _, err := api.ExecBounded(context.Background(), `{ session(id:"bad") { events { text } } }`, "", nil, config.Defaults().Tools.GraphQL); err == nil {
		t.Fatal("corrupt named read succeeded")
	}
	st, _ := os.Stat(p)
	if !st.ModTime().Before(time.Now().AddDate(0, -3, 0)) {
		t.Fatal("failed projection refreshed last use")
	}
	raw, err := api.Exec(context.Background(), `{ session(id:"missing") { id } }`, "", nil)
	if err != nil || !bytes.Contains(raw, []byte("null")) {
		t.Fatalf("missing behavior %s %v", raw, err)
	}
}
