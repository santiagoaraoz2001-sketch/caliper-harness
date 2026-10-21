package plan

import (
	"strings"
	"testing"

	"specific-harness/internal/store"
)

// Synthetic check/reviewer receipts qualify owner rules, not model performance.
func coverageFixture(t *testing.T, goal, title string) (*store.Store, *Plan, *Approach, *Task) {
	t.Helper()
	s, p, a, task := atomicFixture(t)
	if err := s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.goal=$goal`, map[string]any{"id": p.ID, "goal": goal}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.title=$title`, map[string]any{"id": a.ID, "title": title}); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureApproachTransition(s, a.ID, ApproachReady); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionApproach(s, a.ID, ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionTask(s, task.ID, TaskRunning); err != nil {
		t.Fatal(err)
	}
	recordCompleted(t, s, task.ID)
	if _, err := TransitionTask(s, task.ID, TaskDone); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionApproach(s, a.ID, ApproachComplete); err != nil {
		t.Fatal(err)
	}
	return s, p, a, task
}

func TestPlanCoverageCompatibilityAndInvalidScope(t *testing.T) {
	for _, tc := range []struct {
		name, goal, title string
		qualified         bool
		wantError         string
	}{
		{"legacy-single", `{"articles":["one"]}`, `{"article_index":0,"article":"one","strategy":"fix"}`, true, ""},
		{"multi", `{"articles":["one","two"]}`, `{"coverage":[{"article_index":0,"article":"one"},{"article_index":1,"article":"two"}],"strategy":"fix both"}`, true, ""},
		{"freeform", "ordinary goal", "ordinary approach", true, ""},
		{"unknown-json-fields-retained", `{"articles":["one"],"future":"kept"}`, `{"article_index":0,"article":"one","strategy":"fix","future":"kept"}`, true, ""},
		{"uncovered", `{"articles":["one","two"]}`, `{"article_index":0,"article":"one","strategy":"fix"}`, false, ""},
		{"freeform-approach-does-not-infer-coverage", `{"articles":["one"]}`, "I fixed one", false, ""},
		{"malformed-new-coverage", `{"articles":["one"]}`, `{"coverage":null,"strategy":"fix"}`, false, "coverage"},
		{"null-index", `{"articles":["one"]}`, `{"coverage":[{"article_index":null,"article":"one"}],"strategy":"fix"}`, false, "coverage"},
		{"outdated-text", `{"articles":["changed"]}`, `{"article_index":0,"article":"one","strategy":"fix"}`, false, "no longer matches"},
		{"outside-plan", `{"articles":["one"]}`, `{"coverage":[{"article_index":1,"article":"two"}],"strategy":"fix"}`, false, "no longer matches"},
		{"malformed-articles", `{"articles":"one"}`, `{"article_index":0,"article":"one","strategy":"fix"}`, false, "articles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, _, _ := coverageFixture(t, tc.goal, tc.title)
			c, err := PlanCoverage(s, p.ID)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("coverage error: %v", err)
				}
			} else if err != nil || c.Qualified() != tc.qualified {
				t.Fatalf("coverage=%+v err=%v", c, err)
			}
			_, err = TransitionPlan(s, p.ID, PlanComplete)
			if (err == nil) != tc.qualified {
				t.Fatalf("transition disagrees with coverage: %v", err)
			}
		})
	}
}

func TestPlanCoverageCannotHideUnresolvedOverlappingApproach(t *testing.T) {
	title := `{"coverage":[{"article_index":0,"article":"one"}],"strategy":"fix"}`
	s, p, _, task := coverageFixture(t, `{"articles":["one"]}`, title)
	other, err := CreateApproach(s, p.ID, title)
	if err != nil {
		t.Fatal(err)
	}
	c, err := PlanCoverage(s, p.ID)
	if err != nil || c.Qualified() || len(c.Uncovered) != 0 || len(c.Unresolved) != 1 || c.Unresolved[0].ID != other.ID {
		t.Fatalf("overlap hid declared work: %+v %v", c, err)
	}
	if _, err = TransitionPlan(s, p.ID, PlanComplete); err == nil {
		t.Fatal("overlap allowed completion")
	}
	if _, err = TransitionTask(s, task.ID, TaskFailed); err != nil {
		t.Fatal(err)
	}
	c, err = PlanCoverage(s, p.ID)
	if err != nil || c.Qualified() || len(c.Unresolved) != 2 || len(c.Uncovered) != 1 {
		t.Fatalf("rework retained qualification: %+v %v", c, err)
	}
}

func TestPlanCoveragePreservesQualifiedRetirementAndLegacyArchive(t *testing.T) {
	s, p, a, _ := coverageFixture(t, `{"articles":["one"]}`, `{"article_index":0,"article":"one","strategy":"fix"}`)
	if _, err := TransitionApproach(s, a.ID, ApproachRetired); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanComplete); err != nil {
		t.Fatal(err)
	}
	// Historical completed Plan gains no new qualification from being archived.
	if err := s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.goal=$goal`, map[string]any{"id": p.ID, "goal": `{"articles":["one","historically missing"]}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanRetired); err != nil {
		t.Fatal("legacy archive semantics changed", err)
	}
	c, err := PlanCoverage(s, p.ID)
	if err != nil || c.Qualified() || len(c.Uncovered) != 1 {
		t.Fatal("archive fabricated coverage", c, err)
	}
}
