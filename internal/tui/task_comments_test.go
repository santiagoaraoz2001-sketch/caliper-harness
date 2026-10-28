package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"path/filepath"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestRunningTaskCommentUIRoutesExactOwner(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	root, _ := filepath.EvalSymlinks(dir)
	m := New(testCfg(), "component")
	m.StorePath = filepath.Join(dir, "db")
	s, err := store.OpenWithBufferPool(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlanContext(plan.WithProject(context.Background(), root), s, "actual", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "strategy")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(s, task.ID, "current", "target", "sketch", "true exits0", "test", "verify true", "true", "printf UI_FIXTURE_ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Ready(context.Background(), s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := plan.ReadinessSnapshot(s, a.ID)
	task, err = plan.ClaimTaskContext(plan.WithProject(context.Background(), root), s, task.ID, snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Chat.Running = true
	m.Chat.SessionIDs = []string{"actual-run"}
	m.Input.SetValue("/comment " + task.ID + " caller β🙂")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	actual, _ := plan.GetTask(s, task.ID)
	comments, err := outcome.TaskComments(actual.AttemptLog, 1)
	if err != nil || len(comments) != 1 || comments[0].Source != "user" || comments[0].SessionRef != "actual-run" || !strings.Contains(comments[0].Text, "caller β🙂") || len(m.Comment) != 0 {
		t.Fatalf("UI queued instead of task-owned %+v %v pending%+v", comments, err, m.Comment)
	}
	if !actual.SameBrief(task) || actual.Qualified() {
		t.Fatal("UI comment changed frozen authority")
	}
	if _, err = plan.TransitionTask(s, task.ID, plan.TaskCancelled); err != nil {
		t.Fatal(err)
	}
	before := actual.AttemptLog
	m.Input.SetValue("/comment " + task.ID + " closed target")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	actual, _ = plan.GetTask(s, task.ID)
	if actual.AttemptLog == before || len(m.Comment) != 0 || actual.State != plan.TaskCancelled {
		t.Fatal("late feedback lost or silently reopened work")
	}
}
