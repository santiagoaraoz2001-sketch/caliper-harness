package memory

import (
	"path/filepath"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestKnowledgeReferenceTargetsReadOnceAndOnlyForStatus(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	_, err = PutKnowledgeReferences(t.Context(), s, root, "test", "target", "## a\n"+strings.Repeat("body ", 100)+"\n## b\nOther", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := PutKnowledgeReferences(t.Context(), s, root, "test", "source", "Links", "", "", "", []Reference{{Key: "target", Relation: "explains", Section: "a"}, {Key: "target", Relation: "explains", Section: "b"}, {Key: "target", Relation: "depends_on", Section: "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	consume := func(string) error { reads++; return nil }
	refs, err := References(t.Context(), s, root, entry.ID, false, 0, consume)
	if err != nil || len(refs) != 3 || reads != 0 {
		t.Fatalf("metadata lookup: refs=%v reads=%d err=%v", refs, reads, err)
	}
	refs, err = References(t.Context(), s, root, entry.ID, true, 3000, consume)
	if err != nil || reads != 1 {
		t.Fatalf("status lookup: refs=%v reads=%d err=%v", refs, reads, err)
	}
	resolved, missing := 0, 0
	for _, ref := range refs {
		if ref.Status == "resolved" {
			resolved++
		}
		if ref.Status == "missing-section" {
			missing++
		}
	}
	if resolved != 2 || missing != 1 {
		t.Fatal(refs)
	}
}
