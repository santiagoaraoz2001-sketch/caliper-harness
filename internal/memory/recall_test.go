package memory

import (
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/store"
)

func TestRecallFuzzy(t *testing.T) {
	entries := []*Entry{{Kind: "decision", Body: "approve refunds under ten dollars"}}
	if got := Recall(entries, "refund", 3, 1000, 4); got == "" {
		t.Error("refund must recall the refunds entry")
	}
}

func TestRankFuzzy(t *testing.T) {
	a := &Entry{Kind: "decision", Body: "approve refunds under ten dollars"}
	b := &Entry{Kind: "note", Body: "server rebooted cleanly"}
	if got := Rank([]*Entry{b, a}, "refund", 4); got[0] != a {
		t.Error("refund must rank the refunds entry first")
	}
	if got := Rank([]*Entry{b, a}, "Refund", 4); got[0] != a {
		t.Error("match must ignore case")
	}
}

func TestRankOrdersByOverlap(t *testing.T) {
	es := []*Entry{
		{Kind: "note", Body: "approve refund"},
		{Kind: "event", Body: "user billed twice wants refund"},
	}
	ranked := Rank(es, "billed twice refund", 4)
	if ranked[0].Body != es[1].Body || ranked[1].Body != es[0].Body {
		t.Errorf("wrong order: %q, %q", ranked[0].Body, ranked[1].Body)
	}
}

func TestRankTiesKeepNewestFirst(t *testing.T) {
	es := []*Entry{
		{Kind: "note", Body: "refund approved"},
		{Kind: "note", Body: "refund denied"},
	}
	ranked := Rank(es, "refund", 4)
	if ranked[0].Body != "refund approved" || ranked[1].Body != "refund denied" {
		t.Errorf("tie must keep input order: %q, %q", ranked[0].Body, ranked[1].Body)
	}
}

func TestRecallTopNCap(t *testing.T) {
	es := []*Entry{
		{Kind: "note", Body: "refund approved yesterday"},
		{Kind: "note", Body: "refund denied today"},
	}
	got := Recall(es, "refund", 1, 1000, 4)
	if strings.Count(got, "- [note]") != 1 || !strings.Contains(got, "Historical memory") {
		t.Errorf("topN=1 must pick one: %q", got)
	}
}

func TestRecallCharCapSkips(t *testing.T) {
	es := []*Entry{
		{Kind: "note", Body: "refund approved yesterday morning"},
		{Kind: "note", Body: "refund ok"},
	}
	got := Recall(es, "refund", 3, 12, 4)
	if got != "" {
		t.Errorf("must omit rather than strip provenance: %q", got)
	}
}

func TestRecallFallbackTruncates(t *testing.T) {
	es := []*Entry{{Kind: "event", Body: "reported outage resolved"}}
	got := Recall(es, "outage", 3, 8, 4)
	// maxChars now bounds the complete returned text, not only the body.
	want := ""
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRecallEmptyCases(t *testing.T) {
	es := []*Entry{{Kind: "note", Body: "refund approved"}}
	cases := []struct {
		name         string
		query        string
		topN, max    int
		min          int
		emptyEntries bool
	}{
		{"no match", "aardvark", 3, 1000, 4, false},
		{"disabled", "refund", 0, 1000, 4, false},
		{"stopwords only", "what is the", 3, 1000, 4, false},
		{"short words", "up the db", 3, 1000, 4, false},
		{"no entries", "refund", 3, 1000, 4, true},
	}
	for _, tc := range cases {
		entries := es
		if tc.emptyEntries {
			entries = nil
		}
		if got := Recall(entries, tc.query, tc.topN, tc.max, tc.min); got != "" {
			t.Errorf("%s: got %q want empty", tc.name, got)
		}
	}
}

func TestRecallMinWordZeroKeepsShortWords(t *testing.T) {
	es := []*Entry{{Kind: "note", Body: "db is up"}}
	if got := Recall(es, "db up", 3, 1000, 0); got == "" {
		t.Error("min 0 must match short words")
	}
}

func TestQueryReadsStore(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Add(s, "note", "refund approved", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, "event", "server rebooted", "s1"); err != nil {
		t.Fatal(err)
	}
	got, err := Query(s, "", "refund", 3, 1000, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "refund approved") || strings.Contains(got, "rebooted") {
		t.Errorf("wrong block: %q", got)
	}
	got, err = Query(s, "event", "refund", 3, 1000, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("kind filter must exclude the note: %q", got)
	}
}
