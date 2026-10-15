package recall

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/memory"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func seedStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := memory.Add(s, "decision", "approve refunds under ten dollars", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Add(s, "note", "server rebooted cleanly", "s1"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecallToolFindsMatch(t *testing.T) {
	d := New(Deps{Store: seedStore(t), TopN: 3, MaxChars: 1000, MinWord: 4})
	res, err := d.Execute(context.Background(), tool.Args{"query": "refunds approved"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "approve refunds") || strings.Contains(res.Text, "rebooted") {
		t.Errorf("wrong block: %q", res.Text)
	}
}

func TestRecallToolNoMatch(t *testing.T) {
	d := New(Deps{Store: seedStore(t), TopN: 3, MaxChars: 1000, MinWord: 4})
	res, err := d.Execute(context.Background(), tool.Args{"query": "aardvark migration"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `no memories match "aardvark migration"`; res.Text != want {
		t.Errorf("got %q want %q", res.Text, want)
	}
}

func TestRecallToolKindFilter(t *testing.T) {
	d := New(Deps{Store: seedStore(t), TopN: 3, MaxChars: 1000, MinWord: 4})
	res, err := d.Execute(context.Background(), tool.Args{"query": "refunds approved", "kind": "note"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "no memories match") {
		t.Errorf("kind filter must exclude the decision: %q", res.Text)
	}
}

func TestRecallToolBadKind(t *testing.T) {
	d := New(Deps{Store: seedStore(t), TopN: 3, MaxChars: 1000, MinWord: 4})
	if _, err := d.Execute(context.Background(), tool.Args{"query": "x", "kind": "bogus"}); err == nil ||
		!strings.Contains(err.Error(), "event, decision, note") {
		t.Errorf("bad kind must fail loud: %v", err)
	}
}

func TestRecallToolUnwired(t *testing.T) {
	d := New(Deps{TopN: 3, MaxChars: 1000, MinWord: 4})
	if _, err := d.Execute(context.Background(), tool.Args{"query": "x"}); err == nil ||
		!strings.Contains(err.Error(), "not wired") {
		t.Errorf("nil store must fail loud: %v", err)
	}
}
