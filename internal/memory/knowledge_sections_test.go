package memory

import "testing"

func TestKnowledgeSectionsKeepUniqueAnchorsAndFencedCode(t *testing.T) {
	parts := Sections("## a\nOne\n## a-2\nTwo\n## a\nThree\n````go\n```\n## not-a-heading\n````\n## final\nLast", "", "")
	want := []string{"a", "a-2", "a-3", "final"}
	if len(parts) != len(want) {
		t.Fatalf("sections: %+v", parts)
	}
	for i, p := range parts {
		if p.ID != want[i] {
			t.Fatalf("anchor %d: %s", i, p.ID)
		}
	}
	if got := Sections(UIStandard, "UI excellence", ""); len(got) != 1 || got[0].ID != "hierarchy" {
		t.Fatalf("principles not searchable: %+v", got)
	}
}
