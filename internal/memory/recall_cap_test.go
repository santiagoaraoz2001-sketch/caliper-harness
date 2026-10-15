package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/store"
)

func TestRecallExactUnicodeCrashFixture(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("87-byte/47-rune body at cap80 panicked: %v", p)
		}
	}()
	body := "refund " + strings.Repeat("é", 40)
	if len(body) != 87 || utf8.RuneCountInString(body) != 47 {
		t.Fatal("incorrect public crash fixture")
	}
	want := "Relevant memories:\n- [note] " + body
	if got := Recall([]*Entry{{Kind: "note", Body: body}}, "refund", 3, 80, 4); got != want {
		t.Fatalf("full75-rune block fits80: got=%q want=%q", got, want)
	}
}

func TestRecallReturnedRuneCap(t *testing.T) {
	for _, body := range []string{"refund " + strings.Repeat("x", 40), "refund " + strings.Repeat("é", 40), "refund " + strings.Repeat("🙂", 40)} {
		for _, cap := range []int{0, 1, 2, 7, 8, 12, 28, 29, 30, 74, 75, 80, 100} {
			t.Run(fmt.Sprintf("%c/cap%d", []rune(body)[7], cap), func(t *testing.T) {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("rune-cap panic: %v", p)
					}
				}()
				got := Recall([]*Entry{{Kind: "note", Body: body}}, "refund", 3, cap, 4)
				if got == "" || !utf8.ValidString(got) || cap > 0 && utf8.RuneCountInString(got) > cap {
					t.Fatalf("cap%d returned%d valid=%v: %q", cap, utf8.RuneCountInString(got), utf8.ValidString(got), got)
				}
				if !strings.Contains(got, body) && !strings.Contains(got, "…") {
					t.Fatalf("omitted body without disclosure: %q", got)
				}
				if cap == 1 && got != "…" || cap == 2 && got != "r…" {
					t.Fatalf("tinycap obscured content/disclosure: %q", got)
				}
				if cap == 0 && got != "Relevant memories:\n- [note] "+body {
					t.Fatalf("unlimited normal formatting changed: %q", got)
				}
			})
		}
	}
}

func TestRecallWholeBlocksOmissionAndSeparators(t *testing.T) {
	es := []*Entry{{Kind: "note", Body: "refund " + strings.Repeat("é", 40)}, {Kind: "note", Body: "refund ok"}}
	for cap, want := range map[int]string{
		12: "refund ok\n…",
		37: "refund ok\n…",
		39: "Relevant memories:\n- [note] refund ok\n…",
	} {
		if got := Recall(es, "refund", 3, cap, 4); got != want || utf8.RuneCountInString(got) > cap {
			t.Errorf("cap%d whole-fit preference/omission: got=%q want=%q", cap, got, want)
		}
	}
	short := []*Entry{{Kind: "note", Body: "refund α"}, {Kind: "event", Body: "refund 🙂"}}
	full := "Relevant memories:\n- [note] refund α\n- [event] refund 🙂"
	n := utf8.RuneCountInString(full)
	if got := Recall(short, "refund", 3, n, 4); got != full {
		t.Errorf("exact full block/separator boundary: got=%q want=%q", got, full)
	}
	if got := Recall(short, "refund", 3, n-1, 4); got != "Relevant memories:\n- [note] refund α\n…" {
		t.Errorf("one-below boundary must disclose whole omitted second: %q", got)
	}
}

func TestRecallCompactWholeAndDisabled(t *testing.T) {
	es := []*Entry{{Kind: "note", Body: "refund ok"}}
	if got := Recall(es, "refund", 3, 9, 4); got != "refund ok" {
		t.Fatalf("whole compact body should beat clipped decorative metadata: %q", got)
	}
	for _, cap := range []int{0, 1, 80} {
		if got := Recall(es, "refund", 0, cap, 4); got != "" {
			t.Fatalf("disabled atcap%d: %q", cap, got)
		}
	}
	if got := Recall([]*Entry{{Kind: "note", Body: "q"}}, "q", 3, 1, 0); got != "q" {
		t.Fatalf("one-rune whole body must survive tiny cap: %q", got)
	}
	if got := Recall([]*Entry{{Kind: "note", Body: "refund one"}, {Kind: "note", Body: "refund two"}}, "refund", 1, 100, 4); strings.Contains(got, "…") || strings.Contains(got, "two") {
		t.Fatalf("routine TopN selection must not imply budget truncation: %q", got)
	}
}

func TestQueryUnicodeCapUsesRealStore(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("real stored Unicode recall panicked: %v", p)
		}
	}()
	body := "refund " + strings.Repeat("é", 40)
	if _, err := Add(s, "note", body, "cap-session"); err != nil {
		t.Fatal(err)
	}
	got, err := Query(s, "note", "refund", 3, 80, 4)
	if err != nil || got != "Relevant memories:\n- [note] "+body {
		t.Fatalf("actual store Query got=%q err=%v", got, err)
	}
}
