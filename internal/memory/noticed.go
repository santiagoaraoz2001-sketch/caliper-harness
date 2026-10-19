package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// Noticed uses existing immutable project notes. A distinct key per observation
// lets concurrent workers append without replacing each other's findings.
func Noticed(ctx context.Context, s *store.Store, root, sessionRef, approachID, body, filePath string) (*Entry, error) {
	if strings.TrimSpace(approachID) == "" || strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("noticed needs an actual approachId and a nonempty observation")
	}
	key := "noticed/" + approachID + "/" + uuid.NewString()
	return putKnowledge(ctx, s, root, sessionRef, key, body, "", "", filePath, approachID)
}

// Notices shares knowledge's selected-field projection and existing budgets.
// The key prefix is an association, not proof of a defect or workflow authority.
func Notices(ctx context.Context, s *store.Store, root, approachID string, limit, bytes int, body bool, fields []string) ([]*Entry, error) {
	if root == "" {
		return nil, fmt.Errorf("notices project scope unavailable")
	}
	if err := plan.CheckProject(s, "approach", approachID, root); err != nil {
		return nil, err
	}
	return graphList(ctx, s, "", "", limit, bytes, body, root, "", "", fields, KnowledgeSelection{KeyPrefix: "noticed/" + approachID + "/"})
}
