package launch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
)

// Verdicts are controlled; actual owner transitions/refusal and admission bytes
// are real. This proves input/ownership contracts, not natural judge quality.
func TestReadinessRequirementsRemainProtectedAndBudgeted(t *testing.T) {
	s, aid, d := gateDeps(t, 1)
	p, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.GetApproach(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := plan.ApproachTasks(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	latest, reads := "Start empty — 金 β🙂", 0
	d.ReadinessRequirements = func() string { reads++; return latest }
	req, size, err := coherenceRequest(d, p, a, tasks)
	if err != nil || !strings.Contains(req.Context, latest) || reads != 1 {
		t.Fatalf("requirements not captured once: %+v %v reads%d", req, err, reads)
	}
	wire, _ := json.Marshal(req)
	if size != len(wire) {
		t.Fatalf("request budget excludes requirements: %d/%d", size, len(wire))
	}
	judged := false
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		judged = true
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	// No byte ceiling: the complete captured context reaches the judge.
	if err := ReadyCheck(t.Context(), d, aid); err != nil || !judged {
		t.Fatalf("context did not reach judge: %v judged%t", err, judged)
	}
	after, _ := plan.GetTask(s, tasks[0].ID)
	if after.State != plan.TaskQueued || after.Attempts != 0 {
		t.Fatal("admission failure claimed work", after)
	}
}

func TestUnsureReadinessDisplaysSameCapturedRequirements(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	reads := 0
	d.ReadinessRequirements = func() string {
		reads++
		if reads == 1 {
			return "Keep empty — café"
		}
		return "UNCAPTURED LATER REQUEST"
	}
	d.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		if !strings.Contains(req.Context, "Keep empty — café") {
			t.Fatal(req.Context)
		}
		return decision.Verdict{Option: servesGoal, Confidence: .6}, nil
	}
	d.ConfirmReadiness = func(_ context.Context, _, _, brief, prompt string) (bool, error) {
		if !strings.Contains(brief, "Keep empty — café") || !strings.Contains(prompt, "Keep empty — café") || strings.Contains(prompt, "UNCAPTURED") {
			t.Fatalf("display differs from judged request: %s", prompt)
		}
		return false, nil
	}
	if err := ReadyCheck(t.Context(), d, aid); err == nil || reads != 1 {
		t.Fatalf("unsure decline/read count: %v/%d", err, reads)
	}
}
