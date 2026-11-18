package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A summary that cannot reduce retained context while the context still
// fits the window defers compaction instead of failing the run: the
// original continuation is returned intact, and the identical context is
// not re-summarized on the next attempt.
func TestCompactionNonReductionDefersWhileContextFits(t *testing.T) {
	c := truthFixture()
	m := &scriptModel{replies: []string{strings.Repeat("summaryword ", 4000)}}
	r := truthRunner(t, m, 100000)
	next, _, ran, err := r.Compact(context.Background(), c)
	if !errors.Is(err, errCompactionDeferred) {
		t.Fatalf("non-reducing summary must defer, got ran=%v err=%v", ran, err)
	}
	if ran {
		t.Fatal("deferred compaction must not publish")
	}
	if len(next.Entries) != len(c.Entries) {
		t.Fatalf("deferred compaction changed entries: %d != %d", len(next.Entries), len(c.Entries))
	}
	if _, _, ran, err := r.Compact(context.Background(), next); err != nil || ran {
		t.Fatalf("identical context must stay deferred without re-summarizing: ran=%v err=%v", ran, err)
	}
	if len(m.seen) != 1 {
		t.Fatalf("summary model called %d times, want 1", len(m.seen))
	}
}
