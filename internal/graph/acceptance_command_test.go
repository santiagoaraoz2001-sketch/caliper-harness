package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestGraphAcceptanceCommandPreflightAndProjection(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, "test", t.TempDir()).Scoped(t.TempDir(), "authored", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"current", "target", "sketch", "acceptance", "tests", "verify", "checkCommand", "acceptanceCommand"} {
		for _, missing := range []bool{true, false} {
			input := freshTaskValues()
			if missing {
				delete(input, field)
			} else {
				input[field] = " \n"
			}
			out, err := api.ExecBounded(context.Background(), prepareDocument, "", map[string]any{"input": input}, config.Defaults().Tools.GraphQL)
			plans, e := plan.ListPlans(s, "")
			if err == nil || e != nil || len(plans) != 0 {
				t.Fatalf("invalid %s missing=%t created PAT: %s plans%d err%v/%v", field, missing, out, len(plans), err, e)
			}
			var receipt Result
			if e = json.Unmarshal(out, &receipt); e != nil {
				t.Fatal(e)
			}
			for _, effect := range receipt.Effects {
				if effect.Status != "unexecuted" || effect.ID != "" {
					t.Fatalf("blank/missing %s invented effects: %+v", field, receipt.Effects)
				}
			}
		}
	}
	input := freshTaskValues()
	input["acceptance"] = "Regression test demonstrating chained question display" // Exact retained G refusal.
	command := "test ! -e 'forbidden-κ'"
	input["acceptanceCommand"] = command
	out, err := api.ExecBounded(context.Background(), prepareDocument, "", map[string]any{"input": input}, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Result
	if err = json.Unmarshal(out, &receipt); err != nil || len(receipt.Effects) != 4 {
		t.Fatalf("preparation lost actual effects: %s %v", out, err)
	}
	for _, effect := range receipt.Effects {
		if effect.Status != "applied" || effect.ID == "" {
			t.Fatalf("preparation invented effects: %+v", receipt.Effects)
		}
	}
	var data struct {
		Fresh struct {
			Task struct{ ID, Acceptance, CheckCommand, AcceptanceCommand string }
		}
	}
	json.Unmarshal(receipt.Data, &data)
	task, err := plan.GetTask(s, data.Fresh.Task.ID)
	if err != nil || task.Acceptance != input["acceptance"] || data.Fresh.Task.Acceptance != task.Acceptance || task.CheckCommand != "true" || task.AcceptanceCommand != command || data.Fresh.Task.AcceptanceCommand != command || task.Attempts != 0 {
		t.Fatalf("authored command changed or executed: %s %+v %v", out, task, err)
	}
	revised := "test ! -e 'another-κ'"
	q := fmt.Sprintf(`mutation{setTaskBlock(id:%q,current:"observed",target:"correct",sketch:"repair",acceptance:%q,tests:"tests",verify:"review",acceptanceCommand:%q){checkCommand acceptanceCommand}}`, task.ID, task.Acceptance, revised)
	out, err = api.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatal(err)
	}
	task, _ = plan.GetTask(s, task.ID)
	if task.AcceptanceCommand != revised || task.CheckCommand != "true" || task.Attempts != 0 {
		t.Fatalf("optional setter changed omitted command or executed: %s %+v", out, task)
	}
	// Real owners admit this complete prose, freeze its brief and require an explicit claim.
	approach, e := plan.TaskApproach(s, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	parent, e := plan.ApproachPlan(s, approach.ID)
	if e != nil {
		t.Fatal(e)
	}
	ctx := plan.WithProject(context.Background(), api.CapturedProject())
	if _, e = plan.TransitionPlanContext(ctx, s, parent.ID, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	judged := false
	ready, e := plan.Ready(ctx, s, approach.ID, func(_ context.Context, _ *plan.Plan, tasks []*plan.Task) error {
		judged = true
		if len(tasks) != 1 || !task.SameBrief(tasks[0]) {
			t.Fatal("ready checker did not receive actual complete brief")
		}
		return nil
	})
	if e != nil || !judged || ready.State != plan.ApproachReady {
		t.Fatalf("owning Ready refused nonblank acceptance: %+v judged%t %v", ready, judged, e)
	}
	if _, e = plan.SetTaskBlock(s, task.ID, task.Current, task.Target, task.Sketch, "changed", task.Tests, task.Verify, task.CheckCommand, task.AcceptanceCommand); e == nil {
		t.Fatal("Ready brief became editable")
	}
	snapshot, e := plan.ReadinessSnapshot(s, approach.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := plan.ClaimTaskContext(ctx, s, task.ID, snapshot, task.CheckCommand)
	if e != nil || !task.SameBrief(claimed) || claimed.Attempts != 1 || claimed.Checked() || claimed.Qualified() {
		t.Fatalf("claim changed brief or invented verification: %+v %v", claimed, e)
	}
	host, err := api.Scoped(api.CapturedProject(), "host-introspection", false)
	if err != nil {
		t.Fatal(err)
	}
	out, err = host.Exec(context.Background(), `{__type(name:"FreshTaskInput"){inputFields{name type{kind name ofType{name}}}} task(id:"`+task.ID+`"){acceptanceCommand}}`, "", nil)
	if err != nil || !strings.Contains(string(out), `"name":"acceptanceCommand"`) || !strings.Contains(string(out), `"kind":"NON_NULL"`) {
		t.Fatalf("public input/query missing: %s %v", out, err)
	}
}
