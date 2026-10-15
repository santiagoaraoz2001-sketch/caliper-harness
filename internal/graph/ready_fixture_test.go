package graph

import (
	"context"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// Mechanical fixture checker only: no semantic/model readiness claim.
func fixtureApproachTransition(s *store.Store, id, to string) (*plan.Approach, error) {
	if to == plan.ApproachReady {
		return plan.Ready(context.Background(), s, id, func(context.Context, *plan.Plan, []*plan.Task) error { return nil })
	}
	return plan.TransitionApproach(s, id, to)
}

// Existing lifecycle fixtures explicitly opt into a mechanical checker.
func fixtureGraph(s *store.Store, version, sessions string) *API {
	return NewWithReady(s, version, sessions, func(ctx context.Context, id string) (*plan.Approach, error) {
		return plan.Ready(ctx, s, id, func(context.Context, *plan.Plan, []*plan.Task) error { return nil })
	})
}
