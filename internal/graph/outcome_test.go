package graph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestGraphOutcomeReadbackAndLegacyQualification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "p", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	task.Result = "full worker artifact"
	task.Review = "raw ambiguous review"
	task.Execution = outcome.Completed
	task.Verification = outcome.Unverified
	task.VerificationReason = "review unavailable"
	task.AttemptLog = `[{"Kind":"execution","Output":"prior partial"}]`
	if err := plan.SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, task.ID, plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, task.ID, plan.TaskDone); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := New(s, "component", t.TempDir())
	raw, err := api.Exec(context.Background(), `query($id:ID!){approach(id:$id){state qualified tasks{state execution executionReason verification verificationReason review attemptLog qualified result}}}`, "", map[string]any{"id": a.ID})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Approach struct {
			Qualified bool
			Tasks     []struct {
				State, Execution, ExecutionReason, Verification, VerificationReason, Review, AttemptLog, Result string
				Qualified                                                                                       bool
			}
		}
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Approach.Tasks) != 1 {
		t.Fatalf("readback=%s", raw)
	}
	actual := got.Approach.Tasks[0]
	if got.Approach.Qualified || actual.Qualified || actual.Execution != "completed" || actual.Verification != "unverified" || actual.Result != task.Result || actual.Review != task.Review || actual.AttemptLog != task.AttemptLog || actual.VerificationReason != task.VerificationReason {
		t.Fatalf("graph lost/qualified facts: %s", raw)
	}
	// A historical complete lifecycle remains publicly complete but explicitly unqualified.
	if err := s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state='complete'`, map[string]any{"id": a.ID}); err != nil {
		t.Fatal(err)
	}
	raw, err = api.Exec(context.Background(), `query($id:ID!){approach(id:$id){state qualified}}`, "", map[string]any{"id": a.ID})
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Approach struct {
			State     string
			Qualified bool
		}
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Approach.State != "complete" || legacy.Approach.Qualified {
		t.Fatalf("legacy machine success: %s", raw)
	}
}
