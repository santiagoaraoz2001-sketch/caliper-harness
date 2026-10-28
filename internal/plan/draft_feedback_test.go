package plan

import (
	"specific-harness/internal/outcome"
	"testing"
)

func TestDraftFeedbackDoesNotRequireOrGrantExecution(t *testing.T) {
	s, p, a, task := atomicFixture(t)
	root := t.TempDir()
	ctx := WithProject(t.Context(), root)
	if _, err := BindProject(ctx, s, p.ID, root); err != nil {
		t.Fatal(err)
	}
	before, err := ReadinessSnapshot(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	comment, err := AppendUserTaskFeedback(ctx, s, task.ID, 0, "review-session", "Use the approved type scale; plan only")
	if err != nil || comment.Attempt != 0 {
		t.Fatalf("draft feedback refused %+v %v", comment, err)
	}
	after, err := ReadinessSnapshot(s, a.ID)
	if err != nil || after != before {
		t.Fatal("feedback changed readiness authority", err)
	}
	current, err := GetTask(s, task.ID)
	if err != nil || current.State != TaskQueued || current.Attempts != 0 || !current.SameBrief(task) {
		t.Fatal("feedback altered execution", err)
	}
	comments, err := outcome.TaskComments(current.AttemptLog, 0)
	if err != nil || len(comments) != 1 || comments[0].Text != comment.Text {
		t.Fatal("feedback not saved", err)
	}
	if err := RecordCommentDelivery(ctx, s, *comment, "worker"); err == nil {
		t.Fatal("draft feedback granted worker delivery authority")
	}
	if _, err := AppendTaskComment(ctx, s, task.ID, 0, "model-authored", "model", "attempt claim"); err == nil {
		t.Fatal("model append bypassed live claim")
	}
}

func TestReadinessPresentationCannotRelabelStaleBrief(t *testing.T) {
	s, p, a, task := atomicFixture(t)
	tasks, err := ApproachTasks(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadinessBriefSnapshot(s, a.ID, p, a, tasks); err != nil {
		t.Fatal(err)
	}
	current, err := GetTask(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = SetTaskBlock(s, current.ID, current.Current, current.Target, "changed method", current.Acceptance, current.Tests, current.Verify, current.CheckCommand, current.AcceptanceCommand); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadinessBriefSnapshot(s, a.ID, p, a, tasks); err == nil {
		t.Fatal("stale displayed brief received current snapshot")
	}
}
