package agent

import (
	"context"
	"specific-harness/internal/decision"
	"strings"
	"testing"
)

// After one real capacity refusal, later advisory packets on the same
// Runner must be reduced before their first admission attempt instead
// of repeating the build-refuse-rebuild cycle on every settle.
func TestProgressJudgmentLearnsRefusedSizeAndPreReduces(t *testing.T) {
	refusals := 0
	r := &Runner{JudgmentContextChars: 100}
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		if len(q.Context) > 4096 {
			refusals++
			return decision.Verdict{}, &decision.ContextCapacityError{Message: "local byte admission"}
		}
		return decision.Verdict{Option: "continue"}, nil
	}
	call := func() (int, error) {
		packet := struct {
			Intent   string
			Optional []string
		}{Intent: "required intent"}
		for i := 0; i < 200; i++ {
			packet.Optional = append(packet.Optional, strings.Repeat("x", 80))
		}
		before := refusals
		_, _, err := r.progressJudgment(t.Context(), &packet, decision.Request{}, func() bool {
			if len(packet.Optional) == 0 {
				return false
			}
			packet.Optional = packet.Optional[:len(packet.Optional)-1]
			return true
		}, func(string, string) error { return nil })
		return refusals - before, err
	}
	first, err := call()
	if err != nil || first == 0 {
		t.Fatalf("first call refusals=%d err=%v (want at least one refusal to learn from)", first, err)
	}
	second, err := call()
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Fatalf("second call repeated %d capacity refusals; want pre-reduction to zero", second)
	}
}

// When every optional omission is exhausted, the latest settled output
// body is stubbed last (its SHA-256/byte identity stays on the receipt)
// instead of failing the whole advisory judgment as "protected evidence
// exceeds capacity".
func TestReduceForCapacityStubsLatestSettledOutputLast(t *testing.T) {
	exit := 1
	p := progressPacket{
		LatestSettled: &loopObservation{
			CallID: "call-1", Tool: "bash", Failed: true, ExitCode: &exit,
			OutputText: strings.Repeat("y", 30000), OutputBytes: 30000, OutputSHA256: "abc123",
		},
	}
	reduced := false
	for i := 0; i < 6; i++ {
		if p.reduceForCapacity() {
			reduced = true
			break
		}
	}
	if !reduced {
		t.Fatal("reduceForCapacity returned false with only protected latest-settled output remaining")
	}
	if p.LatestSettled.OutputText != "" {
		t.Fatalf("output body not stubbed (%d chars)", len(p.LatestSettled.OutputText))
	}
	if !p.LatestSettled.Failed || p.LatestSettled.OutputSHA256 != "abc123" || p.LatestSettled.OutputBytes != 30000 {
		t.Fatalf("receipt identity lost: %+v", p.LatestSettled)
	}
	if len(p.CapacityOmissions) == 0 {
		t.Fatal("omission not disclosed")
	}
}
