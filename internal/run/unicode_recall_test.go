package run

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// This exercises actual startup recall and the registered on-call tool using
// real Kuzu. New constructs clients but never sends model/judge requests.
func TestNewUnicodeRecallIsBoundedWithoutInference(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("public startup recall boundary panicked: %v", p)
		}
	}()
	body := "refund " + strings.Repeat("é", 40)
	if _, err := memory.Add(s, "note", body, "startup-cap"); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []int{80, 0, 1, 2} {
		cfg := config.Defaults()
		cfg.Recall.MaxChars = cap
		r, notes, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://127.0.0.1:1/v1", Model: "not-requested", MaxSteps: 1, In: strings.NewReader(""), Out: io.Discard}, "refund")
		if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "not applied") || !utf8.ValidString(r.Memories) || cap > 0 && utf8.RuneCountInString(r.Memories) > cap {
			t.Fatalf("startup cap%d: runner=%+v notes=%v err=%v", cap, r, notes, err)
		}
		def, ok := r.Registry.Lookup("recall")
		if !ok {
			t.Fatal("public recall tool missing")
		}
		result, err := def.Execute(context.Background(), tool.Args{"query": "refund", "kind": "note"})
		if err != nil || result.Text != r.Memories {
			t.Fatalf("on-call/startup differ atcap%d: result=%+v err=%v memories=%q", cap, result, err, r.Memories)
		}
		if cap == 80 && !strings.Contains(r.Memories, body) {
			t.Fatal("47-rune body should remain complete at80")
		}
	}
}
