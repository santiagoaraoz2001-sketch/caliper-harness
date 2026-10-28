package run

import (
	"context"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestQualifiedTaskCounterevidenceUsesOwningRevision(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	qualifyPipelineTask(t, f, id)
	f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "task", ID: id}
	before, err := plan.GetTask(f.d.Store, id)
	if err != nil {
		t.Fatal(err)
	}
	d := f.definition()
	if _, _, err = d.Bind(context.Background(), tool.Args{"invoke": "NEXT", "feedback": "broken button"}); err == nil {
		t.Fatal("NEXT swallowed repair feedback")
	}
	f.call(tool.Args{"invoke": "REMEDIATE", "feedback": "actual Accept control is blocked by overlay"})
	after, err := plan.GetTask(f.d.Store, id)
	if err != nil {
		t.Fatal(err)
	}
	if f.scope.ID != id || after.Attempts != before.Attempts || !after.SameBrief(before) || after.Qualified() || !strings.Contains(after.AttemptLog, "actual Accept control") {
		t.Fatalf("revision lost owner/history or retained qualification: %+v", after)
	}
	st, err := planningRead(f.d, f.scope)
	if err != nil || st.Phase != "revision" || st.Revision.TargetID != id {
		t.Fatalf("repair route: %+v %v", st, err)
	}
	f.call(tool.Args{"invoke": "EDIT", "field": "verify", "value": "click actual visible Accept; confirm cargo appears"})
	f.call(tool.Args{"invoke": "COMPLETE"})
	st, err = planningRead(f.d, f.scope)
	if err != nil || st.Phase != "revision_requeue" {
		t.Fatalf("requeue route: %+v %v", st, err)
	}
	f.call(tool.Args{"invoke": "REQUEUE"})
	f.call(tool.Args{"invoke": "COMPLETE"})
	d = f.definition()
	_, args, err := d.Bind(context.Background(), tool.Args{"invoke": "EXECUTE"})
	if err != nil || args["task"] != id {
		t.Fatalf("repair claimed unrelated work: %v %v", args, err)
	}
}
