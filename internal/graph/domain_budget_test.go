package graph

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

func TestDomainAdmissionCountsProjectedAliasesAndKeepsOrdinaryFactsSeparate(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	projectExec(t, a, `mutation {appendKnowledge(key:"project/design/identity",body:"ordinary fact",expectedPreviousId:""){id}}`)
	query := `{ordinary:knowledge(key:"project/design/identity"){body} first:knowledge(key:"caliper/standards/ui",section:"identity"){words:body wordsAgain:body} second:knowledge(key:"caliper/standards/ui",section:"identity"){body}}`
	for _, tokens := range []int{1999, 2000, 2001} {
		calls := 0
		counter := func(_ context.Context, raw string) (int, error) {
			calls++
			for _, want := range []string{`"words":`, `"wordsAgain":`, `"first":`, `"second":`} {
				if !strings.Contains(raw, want) {
					t.Fatalf("projected alias omitted: %s", raw)
				}
			}
			if strings.Contains(raw, "ordinary fact") {
				t.Fatal("ordinary graph fact charged against domain allowance")
			}
			return tokens, nil
		}
		data, err := a.WithDomainTokenCounter(counter).ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
		if calls != 1 {
			t.Fatalf("wanted one aggregate domain count, got%d", calls)
		}
		var result Result
		json.Unmarshal(data, &result)
		if tokens <= 2000 && (err != nil || result.DataOmitted) {
			t.Fatalf("valid boundary refused: %s %v", data, err)
		}
		if tokens > 2000 && (err == nil || !result.DataOmitted || string(result.Data) != `{"ordinary":[{"body":"ordinary fact"}]}` || !strings.Contains(err.Error(), "first[0], second[0]")) {
			t.Fatalf("domain omission changed ordinary sibling or lost omission paths: %s %v", data, err)
		}
	}
	never := a.WithDomainTokenCounter(func(context.Context, string) (int, error) { t.Fatal("ordinary facts invoked tokenizer"); return 0, nil })
	projectExec(t, never, `{knowledge(key:"project/design/identity"){body}}`)
	// Successful nullable siblings must not leak uncounted guidance when another
	// resolver fails. The same counter must survive the public scope wrapper.
	calls := 0
	rebound, err := a.WithDomainTokenCounter(func(context.Context, string) (int, error) { calls++; return 2001, nil }).Scoped(a.scope.root, "second", true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := rebound.ExecBounded(t.Context(), `{knowledge(key:"caliper/standards/ui",section:"identity"){body} plan(id:"missing"){id}}`, "", nil, config.Defaults().Tools.GraphQL)
	var partial Result
	json.Unmarshal(data, &partial)
	if calls != 1 || err == nil || !partial.DataOmitted || strings.Contains(string(partial.Data), "knowledge") {
		t.Fatalf("partial-error domain bypass: count%d %s %v", calls, data, err)
	}
}

func TestDomainOmissionPreservesMixedPageAndMutationSiblings(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	projectExec(t, a, `mutation {appendKnowledge(key:"project/guides/probe/topic",body:"domain-scope-probe article",expectedPreviousId:""){id}}`)
	projectExec(t, a, `mutation {appendKnowledge(key:"project/design/identity",body:"domain-scope-probe ordinary Café 🚚",expectedPreviousId:""){id}}`)
	for _, unavailable := range []bool{false, true} {
		counter := func(context.Context, string) (int, error) {
			if unavailable {
				return 0, errors.New("tokenizer unavailable")
			}
			return 2001, nil
		}
		bounded := a.WithDomainTokenCounter(counter)
		raw, err := bounded.ExecBounded(t.Context(), `{page:knowledge(text:"domain-scope-probe",limit:10){key body}}`, "", nil, config.Defaults().Tools.GraphQL)
		var result Result
		json.Unmarshal(raw, &result)
		want := `{"page":[{"key":"project/design/identity","body":"domain-scope-probe ordinary Café 🚚"}]}`
		var actualPage, expectedPage any
		json.Unmarshal(result.Data, &actualPage)
		json.Unmarshal([]byte(want), &expectedPage)
		if err == nil || !result.DataOmitted || !reflect.DeepEqual(actualPage, expectedPage) || !strings.Contains(err.Error(), "page[") {
			t.Fatalf("same-page ordinary value lost or domain leaked: %s %v", raw, err)
		}
	}
	bounded := a.WithDomainTokenCounter(func(context.Context, string) (int, error) { return 2001, nil })
	raw, err := bounded.ExecBounded(t.Context(), `mutation {guide:appendKnowledge(key:"project/guides/probe/new",body:"too-large-domain",expectedPreviousId:""){id body} ordinary:appendKnowledge(key:"project/testing/strategy",body:"preserve exact ordinary evidence",expectedPreviousId:""){id body}}`, "", nil, config.Defaults().Tools.GraphQL)
	var result Result
	json.Unmarshal(raw, &result)
	if err == nil || !result.DataOmitted || len(result.Effects) != 2 || strings.Contains(string(result.Data), "too-large-domain") || !strings.Contains(string(result.Data), "preserve exact ordinary evidence") || !strings.Contains(err.Error(), "at guide:") {
		t.Fatalf("mixed effects or ordinary mutation receipt lost: %s %v", raw, err)
	}
	stored := projectExec(t, a, `{knowledge(key:"project/guides/probe/new"){body}}`)
	if !strings.Contains(string(stored.Data), "too-large-domain") {
		t.Fatal("omission rolled back committed domain write")
	}
}

func TestProjectGuidesOptInAndDomainFailuresPreserveEffects(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir()).WithDomainTokenCounter(func(context.Context, string) (int, error) { return 0, errors.New("tokenizer unavailable") })
	data, err := a.ExecBounded(t.Context(), `mutation {appendKnowledge(key:"project/guides/mapping/labels",body:"## choices\nUser prefers upright labels.",expectedPreviousId:""){id body}}`, "", nil, config.Defaults().Tools.GraphQL)
	var result Result
	json.Unmarshal(data, &result)
	if err == nil || !result.DataOmitted || len(result.Effects) == 0 {
		t.Fatalf("lost committed effect or claimed delivery: %s %v", data, err)
	}
	// The write happened once; a post-projection budget error is not rollback.
	read := projectAPI(t, s, a.scope.root)
	got := projectExec(t, read, `{knowledge(key:"project/guides/mapping/labels"){body}}`)
	if !strings.Contains(string(got.Data), "upright labels") {
		t.Fatal(string(got.Data))
	}
}
