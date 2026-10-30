package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
)

func TestDomainAuthoringBoundsEachSectionBeforeIDOnlyAppend(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir()).WithDomainTokenCounter(func(_ context.Context, raw string) (int, error) {
		var section struct {
			Section, Body string
			References    []memory.Reference
		}
		if err := json.Unmarshal([]byte(raw), &section); err != nil {
			t.Fatal(err)
		}
		if section.Section == "" {
			return 100, nil
		} // Separate actual retrieval envelope.
		for _, ref := range section.References {
			if ref.Label == "oversized navigation" {
				return 1001, nil
			}
			if ref.Label == "oversized inherited navigation" {
				return 2001, nil
			}
		}
		_, value, ok := strings.Cut(section.Body, "budget=")
		if !ok {
			t.Fatal("missing fixture count", raw)
		}
		n, err := strconv.Atoi(strings.Fields(value)[0])
		return n, err
	})
	key := "project/guides/new-domain/topic"
	appendGuide := func(body, previous string, references string, wantErr bool) Result {
		t.Helper()
		query := fmt.Sprintf(`mutation {appendKnowledge(key:%q,body:%q,expectedPreviousId:%q%s){id}}`, key, body, previous, references)
		raw, err := a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
		var result Result
		if e := json.Unmarshal(raw, &result); e != nil {
			t.Fatal(e)
		}
		if (err != nil) != wantErr {
			t.Fatalf("err=%v response=%s", err, raw)
		}
		if wantErr && (len(result.Effects) != 1 || result.Effects[0].Status != "rejected") {
			t.Fatalf("invalid write not rejected: %s", raw)
		}
		return result
	}
	appendGuide("## choices\nbudget=1001", "", "", true)
	if got := projectExec(t, a, `{knowledge(key:"project/guides/new-domain/topic"){id}}`); string(got.Data) != `{"knowledge":[]}` {
		t.Fatal("first oversized guide saved")
	}
	first := appendGuide("## choices\nbudget=1000", "", "", false).Effects[0].ID
	// A revision can enrich an existing anchor, but cannot grant a new anchor
	// the expanded allowance or consume the previous ID on rejection.
	appendGuide("## choices\nbudget=2000\n## new section\nbudget=1001", first, "", true)
	current := projectExec(t, a, `{knowledge(key:"project/guides/new-domain/topic"){id}}`)
	if !strings.Contains(string(current.Data), first) {
		t.Fatal("rejection changed current immutable version")
	}
	second := appendGuide("## choices\nbudget=2000\n## new section\nbudget=1000", first, "", false).Effects[0].ID
	appendGuide("## choices\nbudget=2001\n## new section\nbudget=1000", second, "", true)
	appendGuide("## choices\nbudget=2000\n## new section\nbudget=2000", second, "", false)
	// Direct navigation is part of a new section's bound even for ID-only output.
	key = "project/guides/new-domain/navigation"
	appendGuide("## choices\nbudget=1000", "", `,references:[{key:"project/guides/new-domain/topic",relation:"explains",sourceSection:"choices",label:"oversized navigation"}]`, true)
	key = "project/guides/new-domain/inherited"
	prior, err := memory.PutKnowledgeReferences(t.Context(), s, a.scope.root, "user", key, "## choices\nbudget=1000", "", "", "", []memory.Reference{{Key: "project/guides/new-domain/topic", Relation: "explains", SourceSection: "choices", Label: "oversized inherited navigation"}})
	if err != nil {
		t.Fatal(err)
	}
	appendGuide("## choices\nbudget=2000", prior.ID, "", true)
	// Ordinary owners are exempt even when their text resembles a domain article.
	ordinary := a.WithDomainTokenCounter(func(context.Context, string) (int, error) {
		t.Fatal("ordinary owner invoked domain tokenizer")
		return 0, nil
	})
	for _, key := range []string{"project/design/identity", "project/testing/strategy", "project/task/fact"} {
		projectExec(t, ordinary, fmt.Sprintf(`mutation {appendKnowledge(key:%q,body:"## choices\nbudget=99999",expectedPreviousId:""){id body}}`, key))
	}
}

func TestDomainReadRejectsOversizedInitialHostDataButKeepsOrdinaryPageValue(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	for _, key := range []string{"project/guides/new-domain/topic", "project/design/identity"} {
		if _, err := memory.PutKnowledge(t.Context(), s, a.scope.root, "user", key, "scope-probe body", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	bounded := a.WithDomainTokenCounter(func(_ context.Context, raw string) (int, error) {
		if strings.Contains(raw, `"section":`) {
			return 1001, nil
		}
		return 100, nil
	})
	raw, err := bounded.ExecBounded(t.Context(), `{page:knowledge(text:"scope-probe"){key body}}`, "", nil, config.Defaults().Tools.GraphQL)
	var result Result
	json.Unmarshal(raw, &result)
	if err == nil || !result.DataOmitted || !strings.Contains(string(result.Data), "project/design/identity") || strings.Contains(string(result.Data), "project/guides/") {
		t.Fatalf("initial read escaped or ordinary value lost: %s %v", raw, err)
	}
}
