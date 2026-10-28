package memory

import (
	"path/filepath"
	"specific-harness/internal/store"
	"testing"
)

func TestLegacyReferenceMigrationPreservesDocumentWideLinks(t *testing.T) {
	path, root := filepath.Join(t.TempDir(), "db"), t.TempDir()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := PutKnowledgeReferences(t.Context(), s, root, "author", "source", "## first\nOne\n## second\nTwo", "", "", "", []Reference{{Key: "missing", Relation: "explains"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"source_section", "label"} {
		if err = s.Write("ALTER TABLE KNOWLEDGE_REF DROP "+column, nil); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	refs, err := References(t.Context(), s, root, entry.ID, true, 1000, nil, "first")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].SourceSection != "" || refs[0].Label != "" || refs[0].Status != "missing" {
		t.Fatal(refs)
	}
	next, err := PutKnowledgeReferences(t.Context(), s, root, "author", "source", "## first\nChanged", entry.ID, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err = References(t.Context(), s, root, next.ID, false, 0, nil, "first")
	if err != nil || len(refs) != 1 {
		t.Fatalf("inherited migrated refs: %v %v", refs, err)
	}
}

func TestSourceSectionIsPartOfReferenceIdentity(t *testing.T) {
	refs := []Reference{{Key: "target", Relation: "explains", SourceSection: "one"}, {Key: "target", Relation: "explains", SourceSection: "two"}}
	if err := validateReferences(refs); err != nil {
		t.Fatal(err)
	}
	refs[1].SourceSection = "one"
	refs[1].Label = "Different label cannot hide duplicate edge"
	if err := validateReferences(refs); err == nil {
		t.Fatal("duplicate source/target edge admitted")
	}
}
