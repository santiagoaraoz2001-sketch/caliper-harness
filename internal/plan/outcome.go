package plan

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"

	"specific-harness/internal/outcome"
	"specific-harness/internal/store"
)

func stringField(r map[string]any, key string) string { v, _ := r[key].(string); return v }
func (t *Task) Qualified() bool {
	return t.State == TaskDone && t.Execution == outcome.Completed && t.Verification == outcome.Passed && t.Checked()
}

// Checked accepts only retained receipts for this exact command and attempt.
func (t *Task) Checked() bool {
	log := t.AttemptLog
	if t.checkProjection != nil {
		log = *t.checkProjection
	}
	return outcome.Checked(log, t.CheckCommand, t.Attempts, t.AcceptanceCommand)
}

// SetTaskOutcome writes retained artifact and outcome facts together. State
// transitions still belong to TransitionTask; closed ancestors refuse writes.
func SetTaskOutcome(s *store.Store, t *Task) error {
	return s.Update(func(tx *store.Tx) error { return setTaskOutcome(tx, t) })
}
func setTaskOutcome(s stateStore, t *Task) error {
	if !outcome.Valid(t.Execution, t.Verification) {
		return fmt.Errorf("invalid task outcome %q/%q", t.Execution, t.Verification)
	}
	if err := openTask(s, t.ID); err != nil {
		return err
	}

	current, err := GetTask(s, t.ID)
	if err != nil {
		return err
	}
	t.AttemptLog, err = outcome.MergeComments(t.AttemptLog, current.AttemptLog)
	if err != nil {
		return err
	}
	if outcome.RevisionInvalidates(t.AttemptLog, t.Attempts) {
		t.Verification = outcome.Unverified
		t.VerificationReason = "explicit revision requires a fresh checked attempt"
	}
	return s.Write(`MATCH (x:Task) WHERE x.id=$id SET x.result=$result,x.review=$review,x.execution=$execution,x.execution_reason=$er,x.verification=$verification,x.verification_reason=$vr,x.attempt_log=$log,x.check_projection=$projection`, map[string]any{"id": t.ID, "result": t.Result, "review": t.Review, "execution": string(t.Execution), "er": t.ExecutionReason, "verification": string(t.Verification), "vr": t.VerificationReason, "log": t.AttemptLog, "projection": outcome.CheckProjection(t.AttemptLog)})
}

// SettleTaskOutcome commits fresh execution/review facts and lifecycle together.
// Reviewer FAIL is a settlement, not an explicit request to reopen old work.
func SettleTaskOutcome(s *store.Store, t *Task, to string, expected ...*Task) error {
	return s.Update(func(tx *store.Tx) error {
		if err := requireNoRevision(tx, "task", t.ID); err != nil {
			return err
		}
		if len(expected) > 0 {
			current, err := GetTask(tx, t.ID)
			if err != nil {
				return err
			}
			e := expected[0]
			if e == nil || current.State != e.State || current.Attempts != e.Attempts || !current.SameBrief(e) {
				return fmt.Errorf("task %q changed before outcome settlement; receipt not applied", t.ID)
			}
		}
		if err := setTaskOutcome(tx, t); err != nil {
			return err
		}
		current, err := GetTask(tx, t.ID)
		if err != nil {
			return err
		}
		if current.State == to {
			return nil
		}
		_, err = transitionTask(tx, t.ID, to, false)
		return err
	})
}

// ApproachQualified deliberately distinguishes historical raw complete state
// from proven completion. Legacy task metadata never implies qualification.
func ApproachQualified(s stateStore, id string) (bool, error) {
	a, err := GetApproach(s, id)
	if err != nil {
		return false, err
	}
	ts, err := ApproachTasks(s, id)
	if err != nil {
		return false, err
	}
	if a.State != ApproachComplete && a.State != ApproachRetired || len(ts) == 0 {
		return false, nil
	}
	for _, t := range ts {
		if !t.Qualified() {
			return false, nil
		}
	}
	return true, nil
}

// AppendTaskComment never modifies the frozen brief or owning check/review facts.
func AppendTaskComment(ctx context.Context, s *store.Store, id string, attempt int, source, sessionRef, text string) (*outcome.TaskComment, error) {
	return appendTaskComment(ctx, s, id, attempt, source, sessionRef, text, false)
}

// AppendUserTaskFeedback preserves late user intent for the exact current attempt.
// It grants no execution or delivery authority and never changes authored work.
func AppendUserTaskFeedback(ctx context.Context, s *store.Store, id string, attempt int, sessionRef, text string, noteIDs ...string) (*outcome.TaskComment, error) {
	return appendTaskComment(ctx, s, id, attempt, "user", sessionRef, text, true, noteIDs...)
}

func appendTaskComment(ctx context.Context, s *store.Store, id string, attempt int, source, sessionRef, text string, feedback bool, noteIDs ...string) (*outcome.TaskComment, error) {
	if ProjectRoot(ctx) == "" || strings.TrimSpace(text) == "" || (source != "user" && source != "model-authored" && source != "api-authored") {
		return nil, fmt.Errorf("task comment needs captured project, append-site source and nonblank text")
	}
	var comment *outcome.TaskComment
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "task", id, ProjectRoot(ctx)); err != nil {
			return err
		}
		var t *Task
		var err error
		if feedback {
			t, err = GetTask(tx, id)
			if err == nil && attempt == 0 && t.Attempts == 0 && t.State == TaskQueued {
				a, parentErr := taskApproach(tx, id)
				if parentErr != nil {
					return parentErr
				}
				p, parentErr := ApproachPlan(tx, a.ID)
				if parentErr != nil {
					return parentErr
				}
				if a.State != ApproachDraft || p.State != PlanDraft && p.State != PlanActive {
					return fmt.Errorf("draft user feedback requires an open unadmitted task brief")
				}
			} else if err == nil && (attempt < 1 || t.Attempts != attempt) {
				err = fmt.Errorf("user feedback requires the exact current task attempt")
			}
		} else {
			t, err = commentTask(tx, id, attempt, false)
		}
		if err != nil {
			return err
		}
		commentID := uuid.NewString()
		if len(noteIDs) > 0 && noteIDs[0] != "" {
			commentID = noteIDs[0]
			existing, err := outcome.TaskComments(t.AttemptLog, attempt)
			if err != nil {
				return err
			}
			for _, prior := range existing {
				if prior.ID == commentID {
					if prior.TaskID != id || prior.Attempt != attempt || prior.Source != source || prior.SessionRef != sessionRef || prior.Text != text {
						return fmt.Errorf("user note identity conflicts with owning feedback")
					}
					copy := prior
					comment = &copy
					return nil
				}
			}
		}
		comment = &outcome.TaskComment{ID: commentID, TaskID: id, Attempt: attempt, Source: source, SessionRef: sessionRef, Text: text, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		log, err := outcome.AppendRecord(t.AttemptLog, struct {
			Kind    string
			Comment *outcome.TaskComment
		}{"comment", comment})
		if err != nil {
			return err
		}
		return tx.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log`, map[string]any{"id": id, "log": log})
	})
	return comment, err
}

// Authorship requires Running; delivery may follow settlement to Done. Both
// retain the same current attempt and admitted ancestry.
func commentTask(s stateStore, id string, attempt int, delivery bool) (*Task, error) {
	if err := openTask(s, id); err != nil {
		return nil, err
	}
	t, err := GetTask(s, id)
	if err != nil {
		return nil, err
	}
	a, err := taskApproach(s, id)
	if err != nil {
		return nil, err
	}
	p, err := ApproachPlan(s, a.ID)
	if err != nil {
		return nil, err
	}
	allowedState := t.State == TaskRunning || delivery && t.State == TaskDone
	if !allowedState || t.Attempts != attempt || a.State != ApproachRunning || a.NeedsReady || p.State != PlanActive {
		states := "Running"
		if delivery {
			states = "Running or Done"
		}
		return nil, fmt.Errorf("comment requires current %s task attempt %d and admitted active ancestry", states, attempt)
	}
	return t, nil
}

// GraphTaskComments applies the existing raw-source bound BEFORE fetching the log.
func GraphTaskComments(ctx context.Context, s *store.Store, id string, attempt, limit, bytes int) ([]outcome.TaskComment, error) {
	rows, err := GraphRows(ctx, s, "task", id, "", "", []string{"attemptLog"}, 1, bytes)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	if large, _ := rows[0]["too_large"].(bool); large {
		return nil, fmt.Errorf("task comments raw source exceeds GraphQL budget")
	}
	comments, err := outcome.TaskComments(stringField(rows[0], "attempt_log"), attempt)
	if len(comments) > limit {
		return nil, fmt.Errorf("task comments exceed requested limit; request a larger limit within GraphQL bounds")
	}
	return comments, err
}

// RecordCommentDelivery retains delivery of an already-authored comment before
// the next worker or reviewer request, including after execution settles Done.
func RecordCommentDelivery(ctx context.Context, s *store.Store, c outcome.TaskComment, recipient string) error {
	return s.UpdateContext(ctx, func(tx *store.Tx) error {
		if err := CheckProject(tx, "task", c.TaskID, ProjectRoot(ctx)); err != nil {
			return err
		}
		t, err := commentTask(tx, c.TaskID, c.Attempt, true)
		if err != nil {
			return err
		}
		comments, err := outcome.TaskComments(t.AttemptLog, c.Attempt)
		if err != nil {
			return err
		}
		found := false
		for _, actual := range comments {
			if actual == c {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("comment is not an actual owning record")
		}
		record := struct {
			Kind, ID, CommentID, SessionRef, CreatedAt string
			Attempt                                    int
		}{"comment_delivery", uuid.NewString(), c.ID, recipient, time.Now().UTC().Format(time.RFC3339Nano), c.Attempt}
		log, err := outcome.AppendRecord(t.AttemptLog, record)
		if err != nil {
			return err
		}
		return tx.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log`, map[string]any{"id": t.ID, "log": log})
	})
}
