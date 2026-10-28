package plan

import (
	"context"
	"reflect"
	"testing"

	"specific-harness/internal/store"
)

func TestSelectedGuidanceInheritanceDistinguishesAbsentAndEmpty(t *testing.T) {
	ref := GuidanceRef{Key: "project/guides/ui/identity", Version: "v1", Section: "hierarchy"}
	selected := `{"guidance":[{"key":"project/guides/ui/identity","version":"v1","section":"hierarchy"}]}`
	for _, tc := range []struct {
		name string
		docs []string
		want []GuidanceRef
	}{
		{"legacy inherits", []string{"legacy task method", selected}, []GuidanceRef{ref}},
		{"member absent inherits", []string{`{"steps":[]}`, selected}, []GuidanceRef{ref}},
		{"explicit empty clears", []string{`{"guidance":[]}`, selected}, []GuidanceRef{}},
		{"most specific wins", []string{selected, `{"guidance":[]}`}, []GuidanceRef{ref}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EffectiveGuidance(tc.docs...)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selection=%+v want=%+v error=%v", got, tc.want, err)
			}
		})
	}
}

func TestSelectedGuidanceFrozenOwnerAdmitsBothRolesAndRefusesChangedSelection(t *testing.T) {
	for _, changed := range []string{"plan", "approach", "task"} {
		t.Run(changed, func(t *testing.T) {
			s, p, a, initial := atomicFixture(t)
			initial, err := GetTask(s, initial.ID)
			if err != nil {
				t.Fatal(err)
			}
			pinned := `{"guidance":[{"key":"project/guides/ui/identity","version":"v1","section":"hierarchy"}]}`
			initial, err = SetTaskBlock(s, initial.ID, initial.Current, initial.Target, pinned, initial.Acceptance, initial.Tests, initial.Verify, initial.CheckCommand, initial.AcceptanceCommand)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(t.Context(), s, a.ID, func(context.Context, *Plan, []*Task) error { return nil }); err != nil {
				t.Fatal(err)
			}
			snapshot, err := ReadinessSnapshot(s, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := ClaimTaskContext(t.Context(), s, initial.ID, snapshot, initial.CheckCommand)
			if err != nil {
				t.Fatal(err)
			}
			worker, err := CaptureDispatchedExecution(t.Context(), s, task)
			if err != nil {
				t.Fatal(err)
			}
			reviewer, err := CaptureReviewExecution(t.Context(), s, task)
			if err != nil {
				t.Fatal(err)
			}
			for role, owner := range map[string]*ExecutionContract{"worker": worker, "reviewer": reviewer} {
				if err := owner.Admit(t.Context(), s); err != nil || owner.Task.Sketch != pinned {
					t.Fatalf("%s current frozen owner refused: %v", role, err)
				}
			}
			selection := `{"guidance":[{"key":"project/guides/ui/identity","version":"v2","section":"hierarchy"}]}`
			if _, err := SetTaskBlock(s, task.ID, task.Current, task.Target, selection, task.Acceptance, task.Tests, task.Verify, task.CheckCommand, task.AcceptanceCommand); err == nil {
				t.Fatal("public draft edit changed a running task's selected guidance")
			}
			// Simulate a concurrent external graph change to verify re-admission;
			// this test-only write is not a model API or a new mutation authority.
			if err := s.Update(func(tx *store.Tx) error {
				switch changed {
				case "plan":
					return tx.Write(`MATCH (p:Plan) WHERE p.id=$id SET p.goal=$value`, map[string]any{"id": p.ID, "value": selection})
				case "approach":
					return tx.Write(`MATCH (a:Approach) WHERE a.id=$id SET a.title=$value`, map[string]any{"id": a.ID, "value": selection})
				default:
					return tx.Write(`MATCH (t:Task) WHERE t.id=$id SET t.sketch=$value`, map[string]any{"id": task.ID, "value": selection})
				}
			}); err != nil {
				t.Fatal(err)
			}
			for role, owner := range map[string]*ExecutionContract{"worker": worker, "reviewer": reviewer} {
				if err := owner.Admit(t.Context(), s); err == nil {
					t.Fatalf("%s admitted changed %s selection", role, changed)
				}
			}
		})
	}
}
