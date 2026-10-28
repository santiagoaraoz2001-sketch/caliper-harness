package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Optional replay consumes the retained post-stop continuation, without a
// provider, process restart or writes to the benchmark workspace.
func TestCapturedCompactionTransportReplay(t *testing.T) {
	path := os.Getenv("CALIPER_COMPACTION_REPLAY_EVENT")
	if path == "" {
		t.Skip("retained continuation event not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event struct{ Text string }
	if err = json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	var c session.Continuation
	if err = json.Unmarshal([]byte(event.Text), &c); err != nil {
		t.Fatal(err)
	}
	r := truthRunner(t, &scriptModel{}, 262144)
	before := c.Clone()
	summaryReserve := strings.Repeat("x", 16<<10)
	full, err := r.compactionRequest(c, event.Text, summaryReserve)
	if err != nil {
		t.Fatal(err)
	}
	r.JudgePreflight = systemOnePreflight()
	if err = r.JudgePreflight(full); err == nil {
		t.Fatal("captured all-history payload unexpectedly fits")
	}
	bounded, err := r.compactionRequest(c, event.Text, summaryReserve)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.JudgePreflight(bounded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c) {
		t.Fatal("captured continuation mutated")
	}
	var p compactionReview
	if err = json.Unmarshal([]byte(bounded.Context), &p); err != nil {
		t.Fatal(err)
	}
	t.Logf("saved post-stop replay: full context=%d bytes, bounded context=%d bytes, omitted retained anchors=%d call IDs=%d; includes16KiB summary headroom; exact System One encoding passes", len(full.Context), len(bounded.Context), p.RetainedOutsideReview.Anchors, p.RetainedOutsideReview.CallIDs)
}

func systemOnePreflight() func(decision.Request) error {
	return decision.New("http://unused.invalid", "d1:3b", "auto", 0).
		WithSelection(decision.Selection{Runtime: "ollama", Protocol: "system_one"}).Preflight
}

func TestCompactionTransportProjectionPreservesOwningFacts(t *testing.T) {
	c := passedCompactionFixture()
	for i := 0; i < 107; i++ {
		c.Add("guidance", "user", fmt.Sprintf("historical advice %d: %s", i, strings.Repeat("unexecuted advice <&> ", 100)), false)
	}
	c.Add("guidance", "user", "LATEST ADVICE MUST SURVIVE", false)
	c.Add("failure", "user", "UNREAD FAILURE MUST SURVIVE", true)
	for i := 0; i < 416; i++ {
		c.Entries = append(c.Entries, c.Entries[7])
		c.Entries[len(c.Entries)-1].Message.ToolCalls = []llm.ToolCall{{ID: fmt.Sprintf("historical-call-%04d", i)}}
	}
	original := c.Clone()
	r := truthRunner(t, &scriptModel{}, 262144)
	r.JudgePreflight = systemOnePreflight()
	req, err := r.compactionRequest(c, "source kept on disk", "Actual current check passed; prior failures remain.")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.JudgePreflight(req); err != nil {
		t.Fatal(err)
	}
	var p compactionReview
	if err = json.Unmarshal([]byte(req.Context), &p); err != nil {
		t.Fatal(err)
	}
	if p.RetainedOutsideReview == nil || p.RetainedOutsideReview.Anchors == 0 || p.RetainedOutsideReview.CallIDs != 417 {
		t.Fatalf("missing truthful omission provenance: %+v", p.RetainedOutsideReview)
	}
	if !reflect.DeepEqual(p.Coding, c.Coding) || !reflect.DeepEqual(p.Assignment, c.Assignment) || !reflect.DeepEqual(p.Workflow, c.Workflow) {
		t.Fatal("owning facts altered")
	}
	for _, want := range []string{"LATEST ADVICE MUST SURVIVE", "UNREAD FAILURE MUST SURVIVE", "Repair converter", "preserve"} {
		if !strings.Contains(req.Context, want) {
			t.Fatalf("protected text missing: %s", want)
		}
	}
	if !reflect.DeepEqual(c, original) {
		t.Fatal("projection mutated the continuation")
	}
}

func TestCompactionTransportPreflightStopsBeforeSummary(t *testing.T) {
	c := passedCompactionFixture()
	c.Add("note", "user", strings.Repeat("USER AUTHORITY <&> ", 10000), false)
	m := &scriptModel{replies: []string{"Must never be generated"}}
	r := truthRunner(t, m, 262144)
	r.JudgePreflight = systemOnePreflight()
	judged := 0
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		judged++
		return decision.Verdict{}, nil
	}
	next, _, ran, err := r.Compact(context.Background(), c)
	var capacity *decision.ContextCapacityError
	if !errors.As(err, &capacity) || ran || len(m.seen) != 0 || judged != 0 || !reflect.DeepEqual(next, c) {
		t.Fatalf("preflight failed: ran=%v generated=%d judged=%d err=%v", ran, len(m.seen), judged, err)
	}
}

func TestCompactionRetryReusesOnlyIdenticalCandidate(t *testing.T) {
	c := passedCompactionFixture()
	m := &scriptModel{replies: []string{"Actual recorded check passed; failed command and user note remain."}}
	r := truthRunner(t, m, 32768)
	r.JudgePreflight = systemOnePreflight()
	calls := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		if calls == 1 {
			return decision.Verdict{}, errors.New("temporary judge unavailable")
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .9}, nil
	}
	first, _, ran, err := r.Compact(context.Background(), c)
	if err == nil || ran || !reflect.DeepEqual(first, c) {
		t.Fatalf("failed judgment applied: %v", err)
	}
	_, _, ran, err = r.Compact(context.Background(), c)
	if err != nil || !ran || calls != 2 || len(m.seen) != 1 {
		t.Fatalf("regenerated valid candidate: ran=%v calls=%d summaries=%d err=%v", ran, calls, len(m.seen), err)
	}

	// A changed current user request must invalidate the cached proposal.
	r.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		return decision.Verdict{}, errors.New("offline")
	}
	_, _, _, _ = r.Compact(context.Background(), c)
	c.Add("note", "user", "Changed current user requirement", false)
	_, _, _, _ = r.Compact(context.Background(), c)
	if len(m.seen) != 3 {
		t.Fatalf("changed authority reused candidate: summaries=%d", len(m.seen))
	}
}
