package memory

import (
	"path/filepath"
	"testing"
	"time"

	"specific-harness/internal/store"
)

func TestMemoryAddListNewestFirst(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()

	first, err := Add(s, "note", "first", "s")
	if err != nil {
		t.Fatalf("add: %s", err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := Add(s, "decision", "second", "s")
	if err != nil {
		t.Fatalf("add: %s", err)
	}
	if first.ID == "" || first.ID == second.ID {
		t.Fatalf("bad ids: %q %q", first.ID, second.ID)
	}

	got, err := List(s, "", "", 0)
	if err != nil {
		t.Fatalf("list: %s", err)
	}
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("want newest-first [%s %s], got %v", second.ID, first.ID, got)
	}
	if got[0].Kind != "decision" || got[0].Body != "second" || got[0].SessionRef != "s" {
		t.Fatalf("wrong entry: %+v", got[0])
	}
}

func TestMemoryEntryOfRejectsDrift(t *testing.T) {
	if _, err := entryOf(map[string]any{"id": "x"}); err == nil {
		t.Fatal("drifted row accepted, want error")
	}
}

func TestMemoryKindValidation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()

	for _, kind := range []string{"event", "decision", "note"} {
		if _, err := Add(s, kind, "b", "s"); err != nil {
			t.Fatalf("kind %q rejected: %s", kind, err)
		}
	}
	for _, kind := range []string{"", "Note", "memory", "event; DROP"} {
		_, err := Add(s, kind, "b", "s")
		if err == nil {
			t.Fatalf("kind %q accepted, want rejection", kind)
		}
		if want := `memory kind must be one of event, decision, note, got "` + kind + `"`; err.Error() != want {
			t.Fatalf("wrong error: got %q, want %q", err.Error(), want)
		}
	}
}

func TestMemoryFilters(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()

	seed := []struct{ kind, body string }{
		{"note", "alpha one"},
		{"decision", "beta two"},
		{"note", "alpha three"},
		{"event", "gamma four"},
	}
	for _, e := range seed {
		if _, err := Add(s, e.kind, e.body, "s"); err != nil {
			t.Fatalf("add: %s", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	bodies := func(entries []*Entry) []string {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Body)
		}
		return out
	}
	checkEqual := func(t *testing.T, name string, got []string, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}

	got, err := List(s, "note", "", 0)
	if err != nil {
		t.Fatalf("kind filter: %s", err)
	}
	checkEqual(t, "kind=note", bodies(got), []string{"alpha three", "alpha one"})

	got, err = List(s, "", "alpha", 0)
	if err != nil {
		t.Fatalf("text filter: %s", err)
	}
	checkEqual(t, "text=alpha", bodies(got), []string{"alpha three", "alpha one"})

	got, err = List(s, "", "", 2)
	if err != nil {
		t.Fatalf("limit: %s", err)
	}
	checkEqual(t, "limit=2", bodies(got), []string{"gamma four", "alpha three"})

	got, err = List(s, "note", "three", 0)
	if err != nil {
		t.Fatalf("combined: %s", err)
	}
	checkEqual(t, "kind+text", bodies(got), []string{"alpha three"})

	got, err = List(s, "decision", "alpha", 0)
	if err != nil {
		t.Fatalf("empty result: %s", err)
	}
	checkEqual(t, "no match", bodies(got), []string{})
}
