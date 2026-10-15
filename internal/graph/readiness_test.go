package graph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"testing"
)

func TestGraphPausedReadinessPersistsAndLegacyStaysUnqualified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "p", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTask(s, a.ID, "t", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlock(s, x.ID, "c", "t", "s", "1", "tests", "verify", "true", "test 1 -eq 1"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{plan.ApproachReady, plan.ApproachRunning, plan.ApproachPaused} {
		if _, err = fixtureApproachTransition(s, a.ID, to); err != nil {
			t.Fatal(err)
		}
	}
	api := fixtureGraph(s, "native", t.TempDir())
	if _, err = api.Exec(context.Background(), `mutation($id:ID!){setTaskBlock(id:$id,current:"changed",target:"t",sketch:"s",acceptance:"1",tests:"tests",verify:"verify"){current}}`, "", map[string]any{"id": x.ID}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api = fixtureGraph(s, "native", t.TempDir())
	check := func(want bool) {
		t.Helper()
		raw, e := api.Exec(context.Background(), `query($id:ID!){approach(id:$id){needsReady state qualified}}`, "", map[string]any{"id": a.ID})
		if e != nil {
			t.Fatal(e)
		}
		var got struct {
			Approach struct {
				NeedsReady bool
				State      string
				Qualified  bool
			}
		}
		if e = json.Unmarshal(raw, &got); e != nil {
			t.Fatal(e)
		}
		if got.Approach.NeedsReady != want || got.Approach.Qualified {
			t.Fatalf("readback %s", raw)
		}
	}
	check(true)
	if _, err = api.Exec(context.Background(), `mutation($id:ID!){transitionApproach(id:$id,to:"ready"){needsReady}}`, "", map[string]any{"id": a.ID}); err != nil {
		t.Fatal(err)
	}
	check(false)
	// Raw legacy complete has no readiness flag and never inherits qualification.
	if err = s.Write(`MATCH(a:Approach) WHERE a.id=$id SET a.needs_ready=NULL,a.state='complete'`, map[string]any{"id": a.ID}); err != nil {
		t.Fatal(err)
	}
	check(false)
}
