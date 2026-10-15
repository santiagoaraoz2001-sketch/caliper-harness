package plan

import (
	"reflect"
	"testing"

	"specific-harness/internal/outcome"
)

func TestOutcomeInvalidFactsRefuseAll(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	task, _ := CreateTask(s, a.ID, "x", "prompt")
	task.Result = "retained artifact"
	task.ExecutionReason = "retained reason"
	if err := SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
	before, err := GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, facts := range [][2]string{{"unknown", "passed"}, {"stopped", "passed"}, {"invalid", "unverified"}, {"completed", "invalid"}} {
		invalid := *before
		invalid.Execution = outcome.Status(facts[0])
		invalid.Verification = outcome.Verification(facts[1])
		invalid.Result = "must not write"
		if err := SetTaskOutcome(s, &invalid); err == nil {
			t.Fatalf("invalid facts accepted: %v", facts)
		}
		after, err := GetTask(s, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("invalid outcome partially changed stored task: before=%+v after=%+v", before, after)
		}
	}
}

func TestOutcomeLegacyParentCompletionAndRetirementRefused(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "p", "g")
	a, _ := CreateApproach(s, p.ID, "a")
	task, _ := CreateTask(s, a.ID, "x", "prompt")
	if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
		t.Fatal(err)
	}
	// Historical lifecycle rows are preserved; new operations may not certify them.
	for _, stmt := range []struct {
		q    string
		args map[string]any
	}{
		{`MATCH(x:Task) WHERE x.id=$id SET x.state='done',x.result='old artifact',x.review='PASS old prose',x.execution=NULL,x.verification=NULL`, map[string]any{"id": task.ID}},
		{`MATCH(a:Approach) WHERE a.id=$aid SET a.state='complete'`, map[string]any{"aid": a.ID}},
	} {
		if err := s.Write(stmt.q, stmt.args); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := TransitionPlan(s, p.ID, PlanComplete); err == nil {
		t.Fatal("new parent completion inherited legacy raw complete")
	}
	if _, err := TransitionApproach(s, a.ID, ApproachRetired); err == nil {
		t.Fatal("legacy completed-only retirement hid unverified task")
	}
	if err := s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.state='complete'`, map[string]any{"id": p.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := TransitionPlan(s, p.ID, PlanRetired); err == nil {
		t.Fatal("historical parent retirement inferred qualification")
	}
	after, _ := GetTask(s, task.ID)
	if after.State != TaskDone || after.Result != "old artifact" || after.Review != "PASS old prose" || after.Execution != outcome.Unknown {
		t.Fatalf("historical rows rewritten: %+v", after)
	}
}

func TestOutcomeEmptyLegacyApproachRefusesCompletionAndRetirement(t *testing.T) {
	for _, state := range []string{ApproachRunning, ApproachComplete} {
		t.Run(state, func(t *testing.T) {
			s := open(t)
			p, err := CreatePlan(s, "legacy parent", "goal")
			if err != nil {
				t.Fatal(err)
			}
			a, err := CreateApproach(s, p.ID, "legacy empty approach")
			if err != nil {
				t.Fatal(err)
			}
			// Controlled historical row, not a claimed normal ready/launch journey.
			if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state=$state`, map[string]any{"id": a.ID, "state": state}); err != nil {
				t.Fatal(err)
			}
			qualified, err := ApproachQualified(s, a.ID)
			if err != nil || qualified {
				t.Fatalf("empty legacy qualification=%v err=%v", qualified, err)
			}
			to := ApproachComplete
			if state == ApproachComplete {
				to = ApproachRetired
			}
			_, moveErr := fixtureApproachTransition(s, a.ID, to)
			after, err := GetApproach(s, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("real Kuzu empty legacy%s→%s err=%v actualstate=%s", state, to, moveErr, after.State)
			if moveErr == nil {
				t.Error("empty approach qualified a completed-only transition")
			}
			if after.State != state {
				t.Errorf("refused historical state mutated: got%s want%s", after.State, state)
			}
		})
	}
}
