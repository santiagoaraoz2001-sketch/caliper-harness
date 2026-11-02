package plan

import "testing"

// A graph created by mistake and never executed must be cleanable:
// draft plans and approaches may retire directly instead of forcing
// activation first. Executed lifecycles keep their existing guards.
func TestDraftPlanAndApproachRetireDirectly(t *testing.T) {
	s := open(t)
	p, e := CreatePlan(s, "orphan", "goal")
	if e != nil {
		t.Fatal(e)
	}
	a, e := CreateApproach(s, p.ID, "orphan approach")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := TransitionApproach(s, a.ID, ApproachRetired); e != nil {
		t.Fatalf("draft approach retire: %v", e)
	}
	if _, e := TransitionPlan(s, p.ID, PlanRetired); e != nil {
		t.Fatalf("draft plan retire: %v", e)
	}
	got, e := GetPlan(s, p.ID)
	if e != nil || got.State != PlanRetired {
		t.Fatalf("plan state %+v %v", got, e)
	}
}
