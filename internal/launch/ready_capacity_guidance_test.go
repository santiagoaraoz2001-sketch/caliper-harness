package launch

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/decision"
)

// C33 fresh benchmark (20261029-204111-6fed): a legitimate large plan made
// the readiness request exceed the decision transport limit. The refusal
// blamed an unreachable server and told the model to ask the user to check
// /manage, which produced a spurious user ask. Capacity refusals must name
// the size cause and the recoverable next step instead.
func TestReadinessCapacityRefusalNamesSizeCause(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{}, &decision.ContextCapacityError{Message: "complete request bytes 79464 exceed limit 65536", EncodedRequest: true}
	}
	err := ReadyCheck(context.Background(), d, aid)
	if err == nil {
		t.Fatal("capacity refusal must not admit readiness")
	}
	msg := strings.ToLower(err.Error())
	for _, want := range []string{"too large", "shorten", "split"} {
		if !strings.Contains(msg, want) {
			t.Errorf("capacity refusal omits %q: %s", want, msg)
		}
	}
	for _, bad := range []string{"unreachable", "/manage", "check the selected decision server"} {
		if strings.Contains(msg, bad) {
			t.Errorf("capacity refusal misdirects with %q: %s", bad, msg)
		}
	}
	// Non-capacity judge failures keep the server guidance.
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{}, errPlain
	}
	err = ReadyCheck(context.Background(), d, aid)
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("plain judge failure lost server guidance: %v", err)
	}
}

var errPlain = &plainError{"connection refused"}

type plainError struct{ s string }

func (e *plainError) Error() string { return e.s }
