package launch

import (
	"context"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestActiveReviewerConsumesOwnedFeedbackAndAcknowledgesOnce(t *testing.T) {
	note := session.UserNote{ID: "stable-user-note", SessionID: "main", Text: "Please explain the remaining limitation β🙂"}
	observed := false
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		if strings.HasPrefix(fixtureRoleRequest(messages), "Review ") {
			for _, m := range messages {
				observed = observed || strings.Contains(m.Content, note.Text)
			}
			return "PASS: actual check receipt supports this fixture", nil
		}
		return "worker result", nil
	})
	d, aid, ids, root := roleHandoffFixture(t, 1, model)
	popped, acks := false, 0
	d.ConfigureReviewer = func(r *agent.Runner) {
		r.BtwNote = func() (session.UserNote, bool) {
			if popped {
				return session.UserNote{}, false
			}
			popped = true
			return note, true
		}
		r.BtwAcknowledge = func(_ context.Context, n session.UserNote) error {
			if n != note {
				t.Fatal("note changed")
			}
			acks++
			return nil
		}
		r.CommentAppendNote = func(ctx context.Context, n session.UserNote) error {
			task, err := plan.GetTask(d.Store, ids[0])
			if err != nil {
				return err
			}
			_, err = plan.AppendUserTaskFeedback(plan.WithProject(ctx, root), d.Store, task.ID, task.Attempts, n.SessionID, n.Text, n.ID)
			return err
		}
	}
	results, err := Launch(t.Context(), d, aid)
	if err != nil || len(results) != 1 || !results[0].Qualified() || !observed || acks != 1 {
		t.Fatalf("review feedback observed=%v acks=%d results=%+v err=%v", observed, acks, results, err)
	}
	task, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	comments, err := outcome.TaskComments(task.AttemptLog, 1)
	if err != nil || len(comments) != 1 || comments[0].ID != note.ID {
		t.Fatalf("feedback owner %+v %v", comments, err)
	}
}
