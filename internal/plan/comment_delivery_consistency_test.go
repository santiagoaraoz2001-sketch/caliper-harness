package plan

import (
	"context"
	"encoding/json"
	"specific-harness/internal/outcome"
	"testing"
)

func TestSettledReviewerCommentDeliveryOwner(t *testing.T) {
	for _, boundary := range []string{"done", "running", "stale-attempt", "forged-record", "wrong-project", "stopped", "closed-approach", "closed-plan", "needs-ready"} {
		t.Run(boundary, func(t *testing.T) {
			s, p, a, task := atomicFixture(t)
			root := t.TempDir()
			ctx := WithProject(t.Context(), root)
			if _, err := BindProject(ctx, s, p.ID, root); err != nil {
				t.Fatal(err)
			}
			if _, err := TransitionPlan(s, p.ID, PlanActive); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(ctx, s, a.ID, func(_ context.Context, _ *Plan, _ []*Task) error { return nil }); err != nil {
				t.Fatal(err)
			}
			digest, _, err := ContractSnapshot(ctx, s, "task", task.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err = ClaimTaskContractContext(ctx, s, task.ID, digest, "true")
			if err != nil {
				t.Fatal(err)
			}
			comment, err := AppendTaskComment(ctx, s, task.ID, task.Attempts, "user", "author-session", "exact instruction α")
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "running" {
				task, err = GetTask(s, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				task.Execution = outcome.Completed
				task.Verification = outcome.Unverified
				if err = SettleTaskOutcome(s, task, TaskDone); err != nil {
					t.Fatal(err)
				}
				if _, err = AppendTaskComment(ctx, s, task.ID, task.Attempts, "user", "author-session", "too late"); err == nil {
					t.Fatal("Done allowed new authorship")
				}
			}
			err = nil
			// Synthetic boundary states isolate the existing owner guards; they do not claim actual execution or review.
			switch boundary {
			case "stale-attempt":
				comment.Attempt++
			case "forged-record":
				comment.Text = "not the owning record"
			case "wrong-project":
				ctx = WithProject(t.Context(), t.TempDir())
			case "stopped":
				err = s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.state=$state`, map[string]any{"id": task.ID, "state": TaskStopped})
			case "closed-approach":
				err = s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.state=$state`, map[string]any{"id": a.ID, "state": ApproachComplete})
			case "closed-plan":
				err = s.Write(`MATCH(p:Plan) WHERE p.id=$id SET p.state=$state`, map[string]any{"id": p.ID, "state": PlanComplete})
			case "needs-ready":
				err = s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.needs_ready=true`, map[string]any{"id": a.ID})
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := GetTask(s, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			err = RecordCommentDelivery(ctx, s, *comment, "review-session")
			after, getErr := GetTask(s, task.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if boundary != "done" && boundary != "running" {
				if err == nil || after.AttemptLog != before.AttemptLog {
					t.Fatalf("refusal boundary %s changed log or admitted: %v", boundary, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !after.SameBrief(before) || after.State != before.State || after.Attempts != before.Attempts || after.Verification != before.Verification || after.Qualified() {
				t.Fatal("delivery changed lifecycle, brief, or qualification")
			}
			var records []struct {
				Kind, CommentID, SessionRef string
				Attempt                     int
			}
			if err = json.Unmarshal([]byte(after.AttemptLog), &records); err != nil {
				t.Fatal(err)
			}
			receipts := 0
			for _, r := range records {
				if r.Kind == "comment_delivery" {
					receipts++
					if r.CommentID != comment.ID || r.SessionRef != "review-session" || r.Attempt != task.Attempts {
						t.Fatalf("wrong delivery receipt %+v", r)
					}
				}
			}
			if receipts != 1 {
				t.Fatalf("got %d owning delivery receipts", receipts)
			}
		})
	}
}
