package run

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	graphapi "specific-harness/internal/graph"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
	graphTool "specific-harness/internal/tools/graph"
)

func locatorFixture(t *testing.T) *planningFixture {
	t.Helper()
	f := newPlanningFixture(t)
	f.d.KnowledgeLimits = config.Defaults().Tools.GraphQL
	f.d.NewModel = func() agent.Model {
		return domainContextCountModel{count: func(context.Context, string) (int, error) { return 100, nil }}
	}
	return f
}

func TestGuidanceLocatorBareAndPrefixedResolveSameVersionAndDeduplicate(t *testing.T) {
	f := locatorFixture(t)
	key := "project/guides/custom/topic"
	entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\nExact body.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.GuidanceRef{{Key: key, Version: entry.ID, Section: "shape"}}
	for _, input := range []string{key + "#shape", "knowledge:" + key + "#shape", " \n" + key + "#shape\nknowledge:" + key + "#shape\n "} {
		refs, err := planningGuidance(t.Context(), f.d, input)
		if err != nil || refs == nil || !reflect.DeepEqual(*refs, want) {
			t.Fatalf("locator=%q refs=%+v err=%v", input, refs, err)
		}
	}
	// A structured reference returned beside an actual section is directly usable
	// as key#section; no semantic search or version-ID translation is involved.
	for _, ref := range memory.BuiltinReferences(memory.UIStandardKey, false) {
		if ref.SourceSection != "identity" {
			continue
		}
		refs, err := planningGuidance(t.Context(), f.d, ref.Key+"#"+ref.Section)
		builtin := memory.BuiltinKnowledge(ref.Key, "", "")
		if err != nil || refs == nil || len(*refs) != 1 || (*refs)[0].Version != builtin.ID {
			t.Fatalf("returned reference unusable: %+v %v", ref, err)
		}
	}
}

func TestGuidanceLocatorIDsUnknownKeysAndAnchorsStillRefuseBeforeEffects(t *testing.T) {
	f := locatorFixture(t)
	entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/guides/custom/topic", "## shape\nExact body.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	builtin := memory.BuiltinKnowledge(memory.UIStandardKey, "", "")
	for _, input := range []string{entry.ID + "#shape", builtin.ID + "#identity", "project/guides/custom/missing#shape", entry.Key + "#unknown", entry.Key + "#", entry.Key + "#shape#extra", "not a locator"} {
		if _, err := planningGuidance(t.Context(), f.d, input); err == nil {
			t.Fatalf("invalid locator accepted: %q", input)
		}
	}
	plans, err := plan.ListPlans(f.d.Store, "")
	if err != nil || len(plans) != 0 {
		t.Fatal("failed selection changed owner store", err)
	}
	if _, _, err := parseGuidanceLocator("not a locator"); err == nil || !strings.Contains(err.Error(), guidanceLocatorExample) {
		t.Fatal("repair omitted filled example", err)
	}
}

func TestGuidanceLocatorSyntaxDoesNotRelaxSectionOrAggregateBudgets(t *testing.T) {
	f := locatorFixture(t)
	key := "project/guides/custom/topic"
	first, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\nInitial.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.d.NewModel = func() agent.Model {
		return domainContextCountModel{count: func(context.Context, string) (int, error) { return memory.DomainInitialTokens + 1, nil }}
	}
	for _, prefix := range []string{"", "knowledge:"} {
		if _, err := planningGuidance(t.Context(), f.d, prefix+key+"#shape"); !errors.Is(err, memory.ErrDomainBudget) {
			t.Fatal("initial section bound relaxed", prefix, err)
		}
	}
	_, err = memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\nEnriched.\n## new section\nNew.", first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.d.NewModel = func() agent.Model {
		return domainContextCountModel{count: func(context.Context, string) (int, error) { return memory.DomainTokens, nil }}
	}
	if _, err := planningGuidance(t.Context(), f.d, key+"#shape\nknowledge:"+key+"#shape"); err != nil {
		t.Fatal("same version duplicated or enriched bound changed", err)
	}
	if _, err := planningGuidance(t.Context(), f.d, key+"#new-section"); !errors.Is(err, memory.ErrDomainBudget) {
		t.Fatal("new anchor got expanded allowance", err)
	}
	// Existing aggregate-overflow controls remain required. This counter admits
	// section items but refuses only the collective serialized items envelope.
	f.d.NewModel = func() agent.Model {
		return domainContextCountModel{count: func(_ context.Context, raw string) (int, error) {
			if strings.Contains(raw, `"items":`) {
				return memory.DomainTokens + 1, nil
			}
			return 100, nil
		}}
	}
	if _, err := planningGuidance(t.Context(), f.d, key+"#shape"); !errors.Is(err, memory.ErrDomainBudget) {
		t.Fatal("aggregate bound bypassed", err)
	}
}

func TestGuidanceBareLocatorKeepsStaleQuestionRefusal(t *testing.T) {
	f := locatorFixture(t)
	f.task()
	entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/guides/custom/topic", "## shape\nExact body.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	bound, actual, err := f.definition().Bind(t.Context(), tool.Args{"current": "old observation", "target": "target", "guidance": entry.Key + "#shape"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.SetTaskBlock(f.d.Store, f.scope.ID, "concurrent observation", "new target", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Execute(t.Context(), actual); err == nil || !strings.Contains(err.Error(), "planning question changed") {
		t.Fatal("stale bare selection committed", err)
	}
}

func TestGuidanceCurrentSchemasExposeLiteralDiscoveryAndFilledLocatorHelp(t *testing.T) {
	f := locatorFixture(t)
	def, err := graphTool.New(graphapi.New(f.d.Store, "test", t.TempDir()), config.Defaults().Tools.GraphQL, f.d.Workspace, "author", f.d.Store.Identity())
	if err != nil {
		t.Fatal(err)
	}
	queryDescription := ""
	for _, param := range def.Params {
		if param.Name == "query" {
			queryDescription = param.Desc
		}
	}
	for _, want := range []string{"literal substring", "one short term", "Follow returned key/section directly"} {
		if !strings.Contains(queryDescription, want) {
			t.Fatal("current shared GraphQL query schema omits discovery mechanics", queryDescription)
		}
	}
	f.call(tool.Args{})
	found := false
	for _, param := range f.definition().Params {
		if param.Name == "guidance" {
			found = true
			if !strings.Contains(param.Desc, guidanceLocatorHelp) {
				t.Fatal("planning schema and parser repair disagree", param.Desc)
			}
		}
	}
	if !found {
		t.Fatal("current planning schema omitted guidance")
	}
}
