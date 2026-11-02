package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/session"
)

// Regression for the candidate35 terminal stop: a continuation dominated by
// protected failure bodies could not compact ("summary does not reduce
// estimated retained context"). Boundary pruning must stub old failure
// bodies so occupancy actually falls and any later compaction can shrink.
func TestFailureHeavyContinuationCompactsAfterPrune(t *testing.T) {
	m := &scriptModel{replies: []string{"Repair pending; actual commands failed. " + strings.Repeat("recorded failure retained as fact. ", 85)}}
	r := truthRunner(t, m, 262144)
	r.Prune = Prune{SchemasKeep: 3, OutputsAgeTokens: 50000}
	r.Compaction.Keep = 10

	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "Build the thing; preserve failures.", false)
	c.Add("output", "user", "tool result:\n"+strings.Repeat("o", 500), false)
	for i := 0; i < 20; i++ {
		c.Add("failure", "user", fmt.Sprintf("tool result:\nACTUAL FAILURE %d\n%s", i, strings.Repeat("x", 4000)), false)
		c.Add("reply", "assistant", "ok", false)
	}
	c.BasisTokens = contentSize(c.Messages())
	c.BasisChars = c.BasisTokens
	before := c.Estimate()

	basis, report := r.prune(&c)
	if report == "" {
		t.Fatal("expected boundary pruning to stub old failure bodies")
	}
	if basis >= before*3/4 {
		t.Fatalf("pruning barely reduced the failure-heavy estimate: before=%d after=%d (%s)", before, basis, report)
	}
	// Loop-faithful: compaction runs only if occupancy still triggers it.
	if shouldCompact(basis, r.MaxTokens, true, r.Compaction) {
		next, _, ran, err := r.Compact(context.Background(), c)
		if err != nil || !ran {
			t.Fatalf("failure-heavy continuation must compact after pruning: ran=%v err=%v", ran, err)
		}
		if got := next.Estimate(); got >= basis {
			t.Fatalf("compaction did not reduce estimate: basis=%d after=%d", basis, got)
		}
		c = next
	}
	// Failure existence survives: stubs and latest verbatim entries remain.
	failures := 0
	for _, e := range c.Entries {
		if e.Kind == "failure" {
			failures++
		}
	}
	if failures == 0 {
		t.Fatal("failure entries vanished instead of surviving as stubs/verbatim")
	}
}
