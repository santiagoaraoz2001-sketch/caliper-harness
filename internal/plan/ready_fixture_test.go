package plan

import (
	"context"
	"specific-harness/internal/store"
)

// Mechanical fixture checker only: no semantic/model readiness claim.
func fixtureApproachTransition(s *store.Store, id, to string) (*Approach, error) {
	if to == ApproachReady {
		return Ready(context.Background(), s, id, func(context.Context, *Plan, []*Task) error { return nil })
	}
	return TransitionApproach(s, id, to)
}
