package run

import (
	"context"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"specific-harness/internal/prompts"
	"strings"
	"testing"

	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
)

// Real owning graph and lifecycle; check/review records below are explicitly
// synthetic navigation fixtures, not native coding or judgment qualification.
func TestGuidedCoverageRawClosureCannotSkipUncoveredArticle(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, id)
	f.call(tool.Args{"invoke": "NEXT_ARTICLE"})
	p, err := plan.GetPlan(f.d.Store, f.scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	gql, _ := f.reg.Lookup("graphql")
	bound, args, err := gql.Bind(context.Background(), tool.Args{"query": `mutation {transitionPlan(id:"` + p.ID + `",to:"complete"){id state}}`})
	if err != nil {
		t.Fatal(err)
	}
	result, err := bound.Execute(context.Background(), args)
	diagnostic := result.Text
	if err != nil {
		diagnostic += err.Error()
	}
	if !strings.Contains(diagnostic, "uncovered article") {
		t.Fatalf("GraphQL did not refuse the missing scope: %s", diagnostic)
	}
	if _, err = plan.TransitionPlanContext(plan.WithProject(context.Background(), f.d.Workspace), f.d.Store, p.ID, plan.PlanComplete); err == nil || !strings.Contains(err.Error(), "article") {
		t.Fatalf("raw closure skipped second article: %v", err)
	}
	after, _ := plan.GetPlan(f.d.Store, p.ID)
	if after.State != plan.PlanActive {
		t.Fatal("refused completion changed Plan")
	}
}

func TestGuidedCoverageOneApproachTwoArticlesCloses(t *testing.T) {
	f := newPlanningFixture(t)
	f.call(tool.Args{})
	f.call(tool.Args{"title": "Two fixes", "outcome": "both requested behaviors", "rules": "preserve neighbors", "articles": "first change\nsecond change", "proof": "paired checks and review"})
	planID := f.scope.ID
	question := f.definition().Schema()
	if !strings.Contains(question, "Article 1: first change") || !strings.Contains(question, "Article 2: second change") {
		t.Fatal(question)
	}
	f.call(tool.Args{"strategy": "repair both shared-owner defects", "coverage": "2, 1, 2"})
	aid := f.scope.ID
	a, _ := plan.GetApproach(f.d.Store, aid)
	refs, strategy, err := plan.ApproachCoverage(a.Title)
	if err != nil || len(refs) != 2 || refs[0].Index != 0 || refs[1].Text != "second change" || strategy != "repair both shared-owner defects" {
		t.Fatalf("bound coverage: %s %v", a.Title, err)
	}
	_, brief, err := plan.ContractSnapshot(plan.WithProject(context.Background(), f.d.Workspace), f.d.Store, "approach", aid)
	if err != nil || !strings.Contains(brief, "second change") {
		t.Fatalf("approval omitted coverage: %s %v", brief, err)
	}
	f.call(tool.Args{"title": "first fix", "objective": "first change"})
	if strings.Contains(f.definition().Purpose, "repair both shared-owner defects") || !strings.Contains(f.definition().Purpose, `approach(id:"`+aid+`"){title}`) || !strings.Contains(f.definition().Purpose, "Approach.title") {
		t.Fatal("task question omitted actual Approach scope")
	}
	first := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "TASK", "title": "second fix", "objective": "second change"})
	second := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, first)
	qualifyPipelineTask(t, f, second)
	f.call(tool.Args{"invoke": "NEXT_ARTICLE"})
	if f.scope.ID != planID {
		t.Fatal("handoff left owning plan")
	}
	st, err := planningRead(f.d, f.scope)
	if err != nil || st.Phase != "plan_complete" {
		t.Fatalf("duplicate planning required: %s %v", st.Phase, err)
	}
	result := f.call(tool.Args{"invoke": "COMPLETE"})
	if result.Workflow == nil || !result.Workflow.Qualified {
		t.Fatal("owner completion not reported", result)
	}
}

func TestGuidedCoverageRejectsInvalidSelectionBeforeEffects(t *testing.T) {
	f := newPlanningFixture(t)
	f.call(tool.Args{})
	f.call(tool.Args{"title": "Two fixes", "outcome": "both behaviors", "rules": "preserve", "articles": "one\ntwo", "proof": "actual checks"})
	for _, choice := range []string{"", "0", "3", "1.5", "first", ", ,", "1 nope", "1, -2"} {
		d := f.definition()
		if _, _, err := d.Bind(context.Background(), tool.Args{"strategy": "do both", "coverage": choice}); err == nil {
			t.Fatalf("accepted %q", choice)
		}
		approaches, err := plan.PlanApproaches(f.d.Store, f.scope.ID)
		if err != nil || len(approaches) != 0 {
			t.Fatal("invalid coverage had effects", err)
		}
	}
}

func TestGuidedCoverageStaleBoundArticleRefusesBeforeEffects(t *testing.T) {
	f := newPlanningFixture(t)
	f.call(tool.Args{})
	f.call(tool.Args{"title": "Two fixes", "outcome": "both behaviors", "rules": "preserve", "articles": "one\ntwo", "proof": "actual checks"})
	bound, args, err := f.definition().Bind(context.Background(), tool.Args{"strategy": "do both", "coverage": "1,2"})
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic concurrent owner mutation tests the existing snapshot guard.
	if err = f.d.Store.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.goal=$goal`, map[string]any{"id": f.scope.ID, "goal": `{"articles":["changed","two"]}`}); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Execute(context.Background(), args); err == nil {
		t.Fatal("stale article selection applied")
	}
	approaches, _ := plan.PlanApproaches(f.d.Store, f.scope.ID)
	if len(approaches) != 0 {
		t.Fatal("stale association had effects")
	}
}

func TestGuidedCoverageOlderCustomCatalogUsesOnlyDisclosedMissingKey(t *testing.T) {
	f := newPlanningFixture(t)
	var catalog struct {
		Texts map[string]string `yaml:"texts"`
	}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &catalog); err != nil {
		t.Fatal(err)
	}
	delete(catalog.Texts, "planning.answer.coverage")
	catalog.Texts["planning.approach"] = "MY AUTHORED APPROACH QUESTION"
	raw, err := yaml.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "custom.yaml")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := prompts.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Prompts = pc
	f.call(tool.Args{})
	f.call(tool.Args{"title": "Two fixes", "outcome": "both behaviors", "rules": "preserve", "articles": "one\ntwo", "proof": "actual checks"})
	d := f.definition()
	if !strings.Contains(d.Purpose, "MY AUTHORED APPROACH QUESTION") || !strings.Contains(d.Purpose, "using shipped coverage guidance") {
		t.Fatal(d.Purpose)
	}
	found := false
	for _, param := range d.Params {
		if param.Name == "coverage" {
			found = param.Desc == prompts.Builtin().Text("planning.answer.coverage") && !param.Required
		}
	}
	if !found {
		t.Fatal("missing coverage fallback")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("custom catalog rewritten")
	}
}

func TestGuidedCoverageChoiceWhitespaceAndDefault(t *testing.T) {
	st := planningState{Articles: []string{"one", "two"}, ArticleIndex: 1}
	for _, args := range []tool.Args{{"coverage": "2,\n1, 2"}, {"coverage": "1\u00a02"}} {
		refs, err := planningCoverage(st, args)
		if err != nil || len(refs) != 2 || refs[0].Index != 0 || refs[1].Index != 1 {
			t.Fatalf("selection not normalized: %v %v", refs, err)
		}
	}
	refs, err := planningCoverage(st, tool.Args{})
	if err != nil || len(refs) != 1 || refs[0].Index != 1 || refs[0].Text != "two" {
		t.Fatalf("omitted coverage changed suggested scope: %v %v", refs, err)
	}
}
