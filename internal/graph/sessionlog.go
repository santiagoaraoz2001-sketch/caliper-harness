package graph

import (
	"context"
)

// sessionEventMutation appends one session event. Values travel as
// variables, never interpolated, so event text cannot break the query.
const sessionEventMutation = `mutation($sid: ID!, $kind: String!, $text: String!) {
	addSessionEvent(sessionId: $sid, kind: $kind, text: $text) { kind }
}`

// SessionLog implements agent.SessionLog over addSessionEvent mutations:
// the loop's session writes travel the same mutation path as every other
// writer. If per-event executor overhead ever slows the loop, the
// founder-sanctioned fallback swaps this for direct session-package calls.
type SessionLog struct {
	api *API
}

// NewSessionLog wraps api as a session event sink.
func NewSessionLog(api *API) SessionLog {
	return SessionLog{api: api}
}

// Append records one event via mutation.
func (l SessionLog) Append(ctx context.Context, sessionID, kind, text string) error {
	_, err := l.api.Exec(ctx, sessionEventMutation, "", map[string]any{
		"sid": sessionID, "kind": kind, "text": text,
	})
	return err
}
