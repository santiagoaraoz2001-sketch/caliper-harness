package plan

import (
	"context"
	"path/filepath"
	"testing"

	"specific-harness/internal/store"
)

func TestElaborationVersionsAndDecisionLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := filepath.Clean(t.TempDir())
	ctx := WithProject(context.Background(), root)
	p, err := CreatePlanContext(ctx, s, "Deliver", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := CreateApproachContext(ctx, s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}

	first, err := CreateElaboration(ctx, s, "approach", a.ID, "session-a", "purpose one", "inputs one", "evidence one", "risks one", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Status != ElaborationDraft || first.TargetKind != "approach" || first.TargetID != a.ID || first.ProjectRoot != root {
		t.Fatalf("first elaboration = %+v", first)
	}
	if _, err := CreateElaboration(ctx, s, "approach", a.ID, "session-b", "purpose stale", "inputs", "evidence", "risks", ""); err == nil {
		t.Fatal("stale previous ID accepted")
	}
	second, err := CreateElaboration(ctx, s, "approach", a.ID, "session-b", "purpose two", "inputs two", "evidence two", "risks two", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 || second.PreviousID != first.ID {
		t.Fatalf("second elaboration = %+v", second)
	}
	if _, err := DecideElaboration(ctx, s, second.ID, true, "user"); err == nil {
		t.Fatal("draft elaboration decided before submission")
	}
	pending, err := SubmitElaboration(ctx, s, second.ID)
	if err != nil || pending.Status != ElaborationPending {
		t.Fatalf("submit = %+v %v", pending, err)
	}
	approved, err := DecideElaboration(ctx, s, second.ID, true, "user")
	if err != nil || approved.Status != ElaborationApproved || approved.DecisionBy != "user" || approved.DecidedAt.IsZero() {
		t.Fatalf("approve = %+v %v", approved, err)
	}
	versions, err := ListElaborations(ctx, s, "approach", a.ID)
	if err != nil || len(versions) != 2 || versions[0].ID != first.ID || versions[1].ID != second.ID || versions[1].Purpose != "purpose two" {
		t.Fatalf("versions = %+v %v", versions, err)
	}
	latest, err := LatestElaboration(ctx, s, "approach", a.ID)
	if err != nil || latest.ID != second.ID {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	other := WithProject(context.Background(), filepath.Join(root, "other"))
	if _, err := ListElaborations(other, s, "approach", a.ID); err == nil {
		t.Fatal("foreign project read elaborations")
	}
}
