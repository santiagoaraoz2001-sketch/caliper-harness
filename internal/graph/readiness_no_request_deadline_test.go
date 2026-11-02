package graph

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// A readiness (permission) gate runs coherence judging and may wait for a
// human. It must not inherit the generic GraphQL request deadline: with a
// 1-second configured limit and a checker that needs ~2 seconds, the
// transition must still complete under the caller's context. Explicit
// caller cancellation is covered by TestBoundedReadinessPersistsIndependentHostApproval.
func TestBoundedReadinessIgnoresGraphQLRequestDeadline(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := plan.WithProject(t.Context(), root)
	p, err := plan.CreatePlanContext(ctx, s, "p", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTaskContext(ctx, s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlockContext(ctx, s, x.ID, "current", "target", "method", "assertions", "tests", "verify", "true", "true"); err != nil {
		t.Fatal(err)
	}
	api := NewWithReady(s, "fixture", t.TempDir(), func(checkCtx context.Context, id string) (*plan.Approach, error) {
		return plan.Ready(checkCtx, s, id, func(checkCtx context.Context, _ *plan.Plan, _ []*plan.Task) error {
			select {
			case <-checkCtx.Done():
				return checkCtx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		})
	})
	scoped, err := api.Scoped(root, "slow-ready-owner", true)
	if err != nil {
		t.Fatal(err)
	}
	limits := config.Defaults().Tools.GraphQL
	limits.TimeoutSeconds = 1
	out, operationErr := scoped.ExecBounded(ctx, `mutation($id:ID!){transitionApproach(id:$id,to:"ready"){id state}}`, "", map[string]any{"id": a.ID}, limits)
	if operationErr != nil {
		t.Fatalf("slow readiness must not inherit the GraphQL request deadline: %v result=%s", operationErr, out)
	}
	got, err := plan.GetApproach(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.ApproachReady {
		t.Fatalf("state=%s want=%s", got.State, plan.ApproachReady)
	}
}
