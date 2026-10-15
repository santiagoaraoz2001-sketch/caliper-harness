package graph

import (
	"context"
	"path/filepath"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestGraphCheckCommandPreserveClearAndFreeze(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := plan.CreatePlan(s, "p", "g")
	if e != nil {
		t.Fatal(e)
	}
	a, e := plan.CreateApproach(s, p.ID, "a")
	if e != nil {
		t.Fatal(e)
	}
	x, e := plan.CreateTask(s, a.ID, "t", "p")
	if e != nil {
		t.Fatal(e)
	}
	api := New(s, "component", t.TempDir())
	ctx := context.Background()
	base := `mutation($id:ID!){setTaskBlock(id:$id,current:"c",target:"t",sketch:"s",acceptance:"1",tests:"tests",verify:"verify",acceptanceCommand:"test 1 -eq 1"%s){checkCommand}}`
	invoke := func(extra string) (string, error) {
		b, e := api.Exec(ctx, strings.Replace(base, "%s", extra, 1), "", map[string]any{"id": x.ID})
		return string(b), e
	}
	if b, e := invoke(`,checkCommand:"printf CHECK"`); e != nil || !strings.Contains(b, "printf CHECK") {
		t.Fatalf("declare %s %v", b, e)
	}
	if b, e := invoke(""); e != nil || !strings.Contains(b, "printf CHECK") {
		t.Fatalf("omission clobbered %s %v", b, e)
	}
	if b, e := invoke(`,checkCommand:""`); e != nil || !strings.Contains(b, `"checkCommand":""`) {
		t.Fatalf("clear %s %v", b, e)
	}
	if _, e = invoke(`,checkCommand:"printf CHECK"`); e != nil {
		t.Fatal(e)
	}
	if _, e = fixtureApproachTransition(s, a.ID, plan.ApproachReady); e != nil {
		t.Fatal(e)
	}
	snap, e := plan.ReadinessSnapshot(s, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = invoke(`,checkCommand:"printf ALTERED"`); e == nil {
		t.Fatal("ready command edited")
	}
	for _, to := range []string{plan.ApproachRunning, plan.ApproachPaused} {
		if _, e = fixtureApproachTransition(s, a.ID, to); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = invoke(`,checkCommand:"printf ALTERED"`); e != nil {
		t.Fatal(e)
	}
	current, e := plan.ReadinessSnapshot(s, a.ID)
	a, e = plan.GetApproach(s, a.ID)
	if e != nil || snap == current || !a.NeedsReady {
		t.Fatalf("command omitted from readiness %v %+v", e, a)
	}
}
