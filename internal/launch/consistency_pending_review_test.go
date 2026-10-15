package launch

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/plan"
)

// Real owning writes reproduce one comment delivery interleaved with two
// reviewer events. The initial Result predates that delivery, as a live role's
// captured log does; the first sink write merges the actual receipt from Store.
func TestConsistencyPendingReviewReusedAcrossCommentDelivery(t *testing.T) {
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
	ctx := plan.WithProject(t.Context(), root)
	comment, err := plan.AppendTaskComment(ctx, s, ids[0], 1, "user", "actual-user", "keep actual review advice β🙂")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	task.AttemptLog = strings.Replace(task.AttemptLog, `"Kind":"comment"`, `"future" : [ true, { "untouched" : "β🙂" } ], "Kind":"comment"`, 1)
	if err = plan.SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
	r := resultOf(task)
	// Existing delivery ownership commits the real ID/attempt, without changing
	// the worker brief or minting a check/review outcome.
	if err = plan.RecordCommentDelivery(ctx, s, *comment, "actual-reviewer"); err != nil {
		t.Fatal(err)
	}
	actual, err := plan.GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var retained []json.RawMessage
	if err = json.Unmarshal([]byte(actual.AttemptLog), &retained); err != nil || len(retained) != 2 {
		t.Fatalf("actual raw comment/delivery fixture: %s %v", actual.AttemptLog, err)
	}
	d := depsFor(s, nil)
	d.Workspace, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	sink := reviewSink{d: d, r: &r}
	for _, event := range []struct{ kind, text string }{{"prompt", "actual reviewer request"}, {"reply", "actual reviewer response"}} {
		if err = sink.Append(t.Context(), task.ID, event.kind, event.text); err != nil {
			t.Fatal(err)
		}
	}
	var entries []attemptRecord
	if err = json.Unmarshal([]byte(r.AttemptLog), &entries); err != nil {
		t.Fatal(err)
	}
	pendingCount := 0
	for _, entry := range entries {
		if entry.Kind == "review_pending" && entry.Attempt == 1 {
			pendingCount++
		}
	}
	if pendingCount != 1 {
		t.Errorf("one review split into %d pending records after actual delivery", pendingCount)
	}
	pending, err := popPending(&r, "review_pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.ReviewerEvents) != 2 || pending.ReviewerEvents[0].Kind != "prompt" || pending.ReviewerEvents[0].Text != "actual reviewer request" || pending.ReviewerEvents[1].Kind != "reply" || pending.ReviewerEvents[1].Text != "actual reviewer response" {
		t.Errorf("pending finalization lost or reordered actual events: %+v", pending.ReviewerEvents)
	}
	var remaining []json.RawMessage
	if err = json.Unmarshal([]byte(r.AttemptLog), &remaining); err != nil || len(remaining) != 2 || string(remaining[0]) != string(retained[0]) || string(remaining[1]) != string(retained[1]) {
		t.Errorf("pending finalization changed unrelated raw records/order: %s %v", r.AttemptLog, err)
	}
	if err = appendReview(&r, attemptRecord{Kind: "review", Attempt: 1, ReviewerEvents: pending.ReviewerEvents}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.AttemptLog, `"Kind":"review_pending"`) || !strings.Contains(r.AttemptLog, string(retained[0])) || !strings.Contains(r.AttemptLog, string(retained[1])) || r.Qualified() {
		t.Errorf("final record lost actual provenance or claimed qualification: %s", r.AttemptLog)
	}
}
