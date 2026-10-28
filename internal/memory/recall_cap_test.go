package memory

import (
	"path/filepath"
	"specific-harness/internal/store"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecallReturnedRuneCap(t *testing.T) {
	for _, body := range []string{"refund " + strings.Repeat("x", 120), "refund " + strings.Repeat("\u00e9", 120), "refund " + strings.Repeat("\U0001f642", 120)} {
		for _, cap := range []int{0, 1, 2, 8, 30, 80, 100, 160, 1000} {
			got := Recall([]*Entry{{ID: "old-id", Kind: "note", Key: "refund-policy", Body: body}}, "refund", 3, cap, 4)
			if !utf8.ValidString(got) || cap > 0 && utf8.RuneCountInString(got) > cap {
				t.Fatalf("invalid/cap%d: %q", cap, got)
			}
			if got != "" && (!strings.Contains(got, "Historical memory") || !strings.Contains(got, `key="refund-policy"`)) {
				t.Fatalf("provenance or locator stripped: %q", got)
			}
			if got != "" && !strings.Contains(got, body) && !strings.HasSuffix(got, "\u2026") {
				t.Fatalf("unreported truncation %q", got)
			}
			if cap == 0 && !strings.Contains(got, body) {
				t.Fatal("unlimited lost body")
			}
			if cap > 0 && cap < 30 && got != "" {
				t.Fatal("tiny budget surfaced bare instructions")
			}
		}
	}
}

func TestRecallWholeBlocksOmissionAndSeparators(t *testing.T) {
	entries := []*Entry{{Kind: "note", Key: "refund-one", Body: "refund alpha"}, {Kind: "event", Key: "refund-two", Body: "refund beta"}}
	full := Recall(entries, "refund", 2, 0, 4)
	if strings.Count(full, "Historical memory") != 1 || !strings.Contains(full, "\n- [event") {
		t.Fatal(full)
	}
	if got := Recall(entries, "refund", 2, utf8.RuneCountInString(full), 4); got != full {
		t.Fatalf("exact boundary: %q", got)
	}
	if got := Recall(entries, "refund", 1, 1000, 4); strings.Contains(got, "beta") || strings.Contains(got, "\u2026") {
		t.Fatal("TopN not truncation", got)
	}
	if got := Recall(entries, "refund", 0, 1000, 4); got != "" {
		t.Fatal("disabled", got)
	}
}

func TestQueryUnicodeCapUsesRealStore(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := "refund " + strings.Repeat("\u00e9", 40)
	entry, err := Add(s, "note", body, "historical-session")
	if err != nil {
		t.Fatal(err)
	}
	for _, cap := range []int{1, 80, 140, 1000} {
		got, err := Query(s, "note", "refund", 3, cap, 4)
		if err != nil || utf8.RuneCountInString(got) > cap {
			t.Fatalf("real store cap: %q %v", got, err)
		}
		if got != "" && (!strings.Contains(got, entry.ID) || !strings.Contains(got, "Historical memory")) {
			t.Fatalf("lost historic locator: %q", got)
		}
	}
}
