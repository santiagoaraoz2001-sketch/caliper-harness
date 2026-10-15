package agent

import "context"

// SessionLog records run events for persistence. The production
// implementation sends addSessionEvent mutations through the embedded
// GraphQL API; tests use fakes. Nil means no persistence.
type SessionLog interface {
	Append(ctx context.Context, sessionID, kind, text string) error
}
