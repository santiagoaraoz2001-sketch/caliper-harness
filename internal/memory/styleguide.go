package memory

import (
	"context"
	"fmt"
	"strings"

	"specific-harness/internal/store"
)

// StyleguideKey is the reserved knowledge key of the workspace code
// styleguide and rules-of-engagement document. It is an ordinary evolving
// knowledge document: permanent for the captured project directory,
// versioned on every append, and readable through the same GraphQL
// knowledge query whether or not style enforcement is enabled.
const StyleguideKey = "styleguide"

// ReadStyleguide returns the current styleguide head body, or "" when the
// document has not been populated yet.
func ReadStyleguide(ctx context.Context, s *store.Store, root string) (string, error) {
	entries, err := Knowledge(ctx, s, root, StyleguideKey, "", 1, 0, true)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return entries[0].Body, nil
}

// AppendStyleguideRule appends one user-stated rule as a new document
// version, preserving every earlier rule. The compare-and-set head rule
// means a concurrent append fails loudly instead of overwriting.
func AppendStyleguideRule(ctx context.Context, s *store.Store, root, sessionRef, rule string) (*Entry, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil, fmt.Errorf("styleguide rule must not be blank")
	}
	entries, err := Knowledge(ctx, s, root, StyleguideKey, "", 1, 0, true)
	if err != nil {
		return nil, err
	}
	previous, body := "", rule
	if len(entries) > 0 {
		previous = entries[0].ID
		body = entries[0].Body + "\n" + rule
	}
	return PutKnowledge(ctx, s, root, sessionRef, StyleguideKey, body, previous, "", "")
}
