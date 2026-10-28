package process

import (
	"context"
	"strings"
)

const TestRunVariable = "CALIPER_TEST_RUN_ID"

type testRunKey struct{}

// WithTestRun scopes a fresh host receipt identity to one admitted check command.
// It never changes the process-wide environment or rewrites the user's shell text.
func WithTestRun(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, testRunKey{}, id)
}
func TestEnvironment(ctx context.Context, base []string) []string {
	id, _ := ctx.Value(testRunKey{}).(string)
	if id == "" {
		return nil
	}
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, TestRunVariable) {
			out = append(out, entry)
		}
	}
	return append(out, TestRunVariable+"="+id)
}
