package launch

import (
	"encoding/json"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestRoleArchiveDoesNotRewriteGrowingGraphLog(t *testing.T) {
	s, aid, ids := setup(t, 1)
	root := bindFixtureProject(t, s, aid, t.TempDir())
	if _, err := plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.TransitionTask(s, ids[0], plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err := plan.SetTaskAttempts(s, ids[0], 1); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	r := resultOf(task)
	d := depsFor(s, nil)
	d.Workspace = root
	d.SessionsDir = t.TempDir()
	sink := reviewSink{d: d, r: &r}
	if err := sink.Append(t.Context(), task.ID, "prompt", "review request"); err != nil {
		t.Fatal(err)
	}
	first, err := plan.GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := plan.WithProject(t.Context(), root)
	one, err := plan.AppendUserTaskFeedback(ctx, s, task.ID, 1, "parent", "Exact β🙂", "queue-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := plan.AppendUserTaskFeedback(ctx, s, task.ID, 1, "parent", "Exact β🙂", "queue-1")
	if err != nil || *one != *two {
		t.Fatalf("retry duplicated feedback: %+v %+v %v", one, two, err)
	}
	if _, err := plan.AppendUserTaskFeedback(ctx, s, task.ID, 1, "parent", "different", "queue-1"); err == nil {
		t.Fatal("conflicting queue identity accepted")
	}
	before, err := plan.GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := sink.Append(t.Context(), task.ID, "reply", strings.Repeat("actual recorded reply ", 200)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := plan.GetTask(s, task.ID)
	if err != nil || before.AttemptLog != after.AttemptLog {
		t.Fatalf("raw events rewrote graph/comment log %v", err)
	}
	var records []attemptRecord
	if err := json.Unmarshal([]byte(first.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ReviewerSession == "" || len(records[0].ReviewerEvents) != 0 {
		t.Fatalf("unbounded graph phase %+v", records)
	}
	events, err := session.Read(d.SessionsDir, records[0].ReviewerSession)
	if err != nil || len(events) != 23 {
		t.Fatalf("archive=%d err=%v", len(events), err)
	}
	pending, err := popPending(&r, "review_pending")
	if err != nil || pending.ReviewerSession != records[0].ReviewerSession {
		t.Fatal("finalization lost archive ref", err)
	}
}
