package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func TestDomainSectionInitialAndEnrichedBoundaries(t *testing.T) {
	for _, previous := range []string{"", "## other\nExisting other section.", "## choices\nInitial choices."} {
		limit := DomainSectionTokens(previous, "choices")
		want := DomainInitialTokens
		if strings.Contains(previous, "## choices") {
			want = DomainTokens
		}
		if limit != want {
			t.Fatalf("section lineage selected %d, want %d", limit, want)
		}
		for _, count := range []int{want - 1, want, want + 1} {
			_, err := AdmitDomainSections(t.Context(), func(_ context.Context, payload string) (int, error) {
				for _, field := range []string{`"key":"project/guides/custom/topic"`, `"section":"choices"`, `"body":`, `"label":"Follow a useful next hop"`, `"domain_budget_basis":"native-token-ids"`} {
					if !strings.Contains(payload, field) {
						t.Fatalf("section metadata missing %s: %s", field, payload)
					}
				}
				return count, nil
			}, "project/guides/custom/topic", previous, Sections("## choices\n漢🚀", "", ""), []Reference{{Key: "project/guides/custom/deeper", SourceSection: "choices", Label: "Follow a useful next hop"}})
			if (err != nil) != (count > want) {
				t.Fatalf("count=%d limit=%d err=%v", count, want, err)
			}
		}
	}
}

func TestDomainInitialFallbackCountsCompleteUnicodeSection(t *testing.T) {
	for _, size := range []int{DomainInitialTokens, DomainInitialTokens + 1} {
		body := "## choices\n漢🚀"
		initial := 0
		_, err := AdmitDomainSections(t.Context(), func(_ context.Context, payload string) (int, error) {
			initial = len(strings.Replace(payload, DomainNativeTokens, DomainUTF8Bytes, 1))
			return 0, nil
		}, "project/guides/custom/topic", "", Sections(body, "", ""), nil)
		if err != nil {
			t.Fatal(err)
		}
		body += strings.Repeat("x", size-initial)
		basis, err := AdmitDomainSections(t.Context(), func(context.Context, string) (int, error) { return 0, llm.ErrTokenCountingUnsupported }, "project/guides/custom/topic", "", Sections(body, "", ""), nil)
		if basis != DomainUTF8Bytes || (err != nil) != (size > DomainInitialTokens) {
			t.Fatalf("size=%d basis=%s err=%v", size, basis, err)
		}
	}
}

func TestDomainSectionsBudgetIndividuallyWithoutFollowingReferences(t *testing.T) {
	sections := Sections("## first\nStart.\n## second\nContinue.", "", "")
	calls := 0
	_, err := AdmitDomainSections(t.Context(), func(_ context.Context, raw string) (int, error) {
		calls++
		var item map[string]any
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "unrequested linked body") {
			t.Fatal("expanded reference")
		}
		return DomainInitialTokens, nil
	}, "project/guides/new-domain/topic", "", sections, []Reference{{Key: "project/guides/new-domain/deep", Section: "example", SourceSection: "second", Label: "Read the example"}})
	if err != nil || calls != 2 {
		t.Fatalf("whole article incorrectly capped: calls=%d err=%v", calls, err)
	}
}
