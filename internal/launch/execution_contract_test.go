package launch

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
)

// Inline task_start and dispatched workers must reject the same changed owner,
// even when a stale in-memory task and matching command remain available.
func TestInlineAndWorkerShareExecutionContract(t *testing.T) {
	for _, field := range []string{"task.prompt", "task.attempts", "approach.title", "approach.state", "approach.ready", "approach.needs_ready", "plan.goal", "plan.state", "plan.project_root"} {
		t.Run(field, func(t *testing.T) {
			d, aid, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) { return "unused", nil }))
			inline := &Assigned{Deps: d, Root: root}
			identity, _, err := inline.Start(t.Context(), ids[0])
			if err != nil {
				t.Fatal(err)
			}
			ctx := plan.WithProject(t.Context(), root)
			worker, err := plan.CaptureDispatchedExecution(ctx, d.Store, inline.task)
			if err != nil || inline.Admit(ctx) != nil || worker.Admit(ctx, d.Store) != nil {
				t.Fatalf("live owner refused before change: %v", err)
			}
			queries := map[string]string{
				"task.prompt":          `MATCH(t:Task) WHERE t.id=$id SET t.prompt='changed brief'`,
				"task.attempts":        `MATCH(t:Task) WHERE t.id=$id SET t.attempts=t.attempts+1`,
				"approach.title":       `MATCH(a:Approach) WHERE a.id=$id SET a.title='changed strategy'`,
				"approach.state":       `MATCH(a:Approach) WHERE a.id=$id SET a.state='paused'`,
				"approach.ready":       `MATCH(a:Approach) WHERE a.id=$id SET a.state='ready'`,
				"approach.needs_ready": `MATCH(a:Approach) WHERE a.id=$id SET a.needs_ready=true`,
				"plan.goal":            `MATCH(p:Plan) WHERE p.id=$id SET p.goal='changed goal'`,
				"plan.state":           `MATCH(p:Plan) WHERE p.id=$id SET p.state='retired'`,
				"plan.project_root":    `MATCH(p:Plan) WHERE p.id=$id SET p.project_root='foreign project'`,
			}
			kind, _, _ := strings.Cut(field, ".")
			id := map[string]string{"task": ids[0], "approach": aid, "plan": identity.PlanID}[kind]
			if err := d.Store.Write(queries[field], map[string]any{"id": id}); err != nil {
				t.Fatal(err)
			}
			for name, admit := range map[string]func(context.Context) error{"inline": inline.Admit, "worker": func(ctx context.Context) error { return worker.Admit(ctx, d.Store) }} {
				err := admit(ctx)
				pausedWorker := name == "worker" && field == "approach.state"
				if pausedWorker && err != nil || !pausedWorker && err == nil {
					t.Errorf("%s readmission for changed %s: %v (paused in-flight worker=%t)", name, field, err, pausedWorker)
				}
			}
		})
	}
}

func TestClaimFreezesRequiredStagesAcrossSameAttemptResume(t *testing.T) {
	requests := 0
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) {
		requests++
		return "must not run", nil
	}))
	d.CheckReport = checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"required-alpha"}}
	inline := &Assigned{Deps: d, Root: root}
	if _, _, err := inline.Start(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	current, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	frozen, found, err := plan.TaskCheckReport(current)
	if err != nil || !found || !frozen.Equal(d.CheckReport) {
		t.Fatalf("claim omitted immutable stages: %+v %v", frozen, err)
	}
	before := current.AttemptLog
	changed := d
	changed.CheckReport = checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"replacement-beta"}}
	resumed := RunTask(plan.WithProject(t.Context(), root), changed, current, 0)
	if resumed.Err == nil || requests != 0 {
		t.Fatalf("changed same-attempt contract executed: %+v requests=%d", resumed, requests)
	}
	after, err := plan.GetTask(d.Store, ids[0])
	if err != nil || after.AttemptLog != before {
		t.Fatal("refusal replaced frozen attempt", err)
	}
	reconstructed := codingFromCheck(changed, &Result{Attempts: current.Attempts, AttemptLog: current.AttemptLog}, current)
	if !reconstructed.Report.Equal(frozen) {
		t.Fatal("current config replaced admitted stage contract")
	}
	if err := inline.Admit(t.Context()); err != nil {
		t.Fatal("unchanged existing owner lost authority", err)
	}
}

func TestRequiredEvidenceClaimKeepsNestedObligations(t *testing.T) {
	requests := 0
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) { requests++; return "must not run", nil }))
	d.CheckReport = checkreport.Contract{Format: checkreport.EvidenceFormat, Expected: []string{"consumer"}, Stages: []checkreport.StageRequirement{{ID: "consumer", Prerequisites: []checkreport.Prerequisite{{Kind: "path", Value: "fixture"}}, Evidence: []checkreport.EvidenceRequirement{{ID: "visible", Expected: "Original required visible outcome"}}}}}
	wanted := d.CheckReport.Clone()
	inline := &Assigned{Deps: d, Root: root}
	if _, _, err := inline.Start(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	current, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	frozen, found, err := plan.TaskCheckReport(current)
	if err != nil || !found || !frozen.Equal(wanted) {
		t.Fatalf("rich admission lost: %+v %v", frozen, err)
	}
	before := current.AttemptLog
	d.CheckReport.Stages[0].Evidence[0].Expected = "weakened later requirement"
	resumed := RunTask(plan.WithProject(t.Context(), root), d, current, 0)
	if resumed.Err == nil || requests != 0 {
		t.Fatal("same-attempt rich contract change executed")
	}
	after, err := plan.GetTask(d.Store, ids[0])
	if err != nil || after.AttemptLog != before {
		t.Fatal("refusal changed admitted contract")
	}
	reconstructed := codingFromCheck(d, &Result{Attempts: current.Attempts, AttemptLog: before}, current)
	if !reconstructed.Report.Equal(wanted) {
		t.Fatal("current config replaced archived rich obligation")
	}
}
