package auth

import (
	"context"
	"errors"
	"testing"
)

func TestReadinessDecisionRecordedBeforeReturn(t *testing.T) {
	for _, choice := range []bool{false, true} {
		var got []ApprovalEvent
		g := &Gate{Mode: "yolo", AskFunc: func(context.Context, string) bool { return choice }, Record: func(_ context.Context, e ApprovalEvent) error { got = append(got, e); return nil }}
		approved, err := g.ConfirmReadiness(t.Context(), "approach", "exact-snapshot", "raw brief", "readable brief")
		if err != nil || approved != choice || len(got) != 1 || got[0].Approved != choice || got[0].Snapshot != "exact-snapshot" || got[0].Brief != "raw brief" || got[0].Kind != "readiness" {
			t.Fatalf("approved=%v events=%+v err=%v", approved, got, err)
		}
	}
	g := &Gate{AskFunc: func(context.Context, string) bool { return true }, Record: func(context.Context, ApprovalEvent) error { return errors.New("disk full") }}
	if approved, err := g.ConfirmReadiness(t.Context(), "a", "s", "b", "p"); err == nil || approved {
		t.Fatal("unrecorded readiness approved")
	}
	ctx, cancel := context.WithCancel(t.Context())
	var receipt ApprovalEvent
	g = &Gate{AskFunc: func(context.Context, string) bool { cancel(); return true }, Record: func(ctx context.Context, e ApprovalEvent) error {
		if ctx.Err() != nil {
			t.Fatal("cancelled recording context")
		}
		receipt = e
		return nil
	}}
	if approved, err := g.ConfirmReadiness(ctx, "a", "s", "b", "p"); err == nil || approved || !receipt.Cancelled || receipt.Approved {
		t.Fatalf("cancelled decision %+v %v", receipt, err)
	}
}
