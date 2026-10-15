package plan

import (
	"context"
	"strings"
	"testing"
)

func TestReadinessRequiresAuthoredAcceptanceCommand(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "goal", "repair the requested behavior")
	a, _ := CreateApproach(s, p.ID, "repair")
	x, _ := CreateTask(s, a.ID, "task", "original requested behavior")
	if _, err := SetTaskBlock(s, x.ID, "observed", "correct", "repair", "check exit0", "tests", "review", "true"); err != nil {
		t.Fatal(err)
	}
	judged := false
	_, err := Ready(context.Background(), s, a.ID, func(context.Context, *Plan, []*Task) error { judged = true; return nil })
	actual, _ := GetApproach(s, a.ID)
	if err == nil || !strings.Contains(err.Error(), "acceptance_command") || judged || actual.State != ApproachDraft {
		t.Fatalf("missing authored command admitted: state=%s judged=%t err=%v", actual.State, judged, err)
	}
}

func TestAcceptanceCommandFrozenWithReadyAndClaim(t *testing.T) {
	s := open(t)
	p, _ := CreatePlan(s, "goal", "repair the requested behavior")
	a, _ := CreateApproach(s, p.ID, "repair")
	x, _ := CreateTask(s, a.ID, "task", "original requested behavior")
	command := "printf 'behavior κ\\n'"
	x, err := SetTaskBlock(s, x.ID, "observed", "correct", "repair", "check exit0", "tests", "review", "true", command)
	if err != nil || x.AcceptanceCommand != command {
		t.Fatalf("literal command changed: %+v %v", x, err)
	}
	TransitionPlan(s, p.ID, PlanActive)
	_, err = Ready(context.Background(), s, a.ID, func(_ context.Context, _ *Plan, tasks []*Task) error {
		if len(tasks) != 1 || !x.SameBrief(tasks[0]) || tasks[0].AcceptanceCommand != command {
			t.Fatal("reviewed readiness omitted frozen behavior command")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadinessSnapshot(s, a.ID)
	if _, err = SetTaskBlock(s, x.ID, x.Current, x.Target, x.Sketch, x.Acceptance, x.Tests, x.Verify, "true", "changed"); err == nil {
		t.Fatal("Ready command edited")
	}
	TransitionApproach(s, a.ID, ApproachRunning)
	TransitionApproach(s, a.ID, ApproachPaused)
	changed := "printf 'revised behavior\\n'"
	y, err := SetTaskBlockCommandsContext(context.Background(), s, x.ID, x.Current, x.Target, x.Sketch, x.Acceptance, x.Tests, x.Verify, nil, &changed)
	if err != nil || y.CheckCommand != "true" || y.SameBrief(x) {
		t.Fatalf("acceptance-only edit lost command/freeze: %+v %v", y, err)
	}
	if _, err = ClaimTaskContext(context.Background(), s, x.ID, snapshot, "true"); err == nil {
		t.Fatal("claim admitted obsolete command snapshot")
	}
	_, err = Ready(context.Background(), s, a.ID, func(context.Context, *Plan, []*Task) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = ReadinessSnapshot(s, a.ID)
	claimed, err := ClaimTaskContext(context.Background(), s, x.ID, snapshot, "true")
	if err != nil || claimed.AcceptanceCommand != changed || claimed.Attempts != 1 || claimed.Checked() {
		t.Fatalf("claim command authority/receipt changed: %+v %v", claimed, err)
	}
}
