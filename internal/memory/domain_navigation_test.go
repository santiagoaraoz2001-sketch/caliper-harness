package memory

import (
	"context"
	"strings"
	"testing"
)

func TestDomainCatalogHasResolvedSectionSpecificDeepLinks(t *testing.T) {
	for _, key := range BuiltinKeys {
		entry := BuiltinKnowledge(key, "", "")
		if entry == nil || len(Sections(entry.Body, "", "")) == 0 {
			t.Fatalf("empty domain article %s", key)
		}
		for _, ref := range BuiltinReferences(key, true) {
			if ref.Status != "resolved" || ref.SourceSection == "" || ref.Section == "" || strings.TrimSpace(ref.Label) == "" {
				t.Errorf("%s has an unusable deep hop: %+v", key, ref)
			}
		}
	}
}

func TestDomainBudgetUsesTokensAndExplicitNamespace(t *testing.T) {
	payload := []byte(strings.Repeat("長い 🚀 ", 1000))
	if err := AdmitDomain(t.Context(), func(context.Context, string) (int, error) { return 2000, nil }, payload); err != nil {
		t.Fatal(err)
	}
	if err := AdmitDomain(t.Context(), func(context.Context, string) (int, error) { return 2001, nil }, []byte("x")); err == nil {
		t.Fatal("byte length substituted for actual tokens")
	}
	if err := AdmitDomain(t.Context(), nil, payload); err == nil {
		t.Fatal("missing tokenizer silently qualified")
	}
	for _, key := range []string{"project/design/identity", "project/testing/strategy", "project/task/tests", "project/guides/", "project/guides/tests", "project/guides//topic"} {
		if ProjectGuide(key) {
			t.Fatal("ordinary/malformed key classified as guide", key)
		}
	}
	if !ProjectGuide("project/guides/new-domain/topic/deep") {
		t.Fatal("new explicitly authored domain not recognized")
	}
}

func TestDomainGuidesNeverEnterTruncatedFactualRecall(t *testing.T) {
	entries := []*Entry{{ID: "guide", Key: "project/guides/ui/palette", Kind: "note", Body: "amber palette domain advice"}, {ID: "fact", Key: "project/design/identity", Kind: "note", Body: "amber palette project fact"}}
	selected, text := SelectRecall(entries, nil, "amber palette", 3, 1000, 4)
	if len(selected) != 1 || selected[0].Entry.ID != "fact" || strings.Contains(text, "domain advice") {
		t.Fatalf("domain guide escaped section delivery: %s", text)
	}
}
