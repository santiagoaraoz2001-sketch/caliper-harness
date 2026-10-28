package graph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestApproachDisplayPreservesExactContract(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "p", "goal")
	if err != nil {
		t.Fatal(err)
	}
	api := fixtureGraph(s, "test", t.TempDir())
	for _, raw := range []string{`{"coverage":[{"article_index":0,"article":"quoted \"requirement\""}],"strategy":"first\nsecond"}`, `{"article_index":0,"article":"legacy","strategy":"keep"}`, "freeform", `{"coverage":broken`, "literal\x1b[31m", `{"coverage":[],"strategy":"keep","unknown":"retain"}`} {
		a, err := plan.CreateApproach(s, p.ID, raw)
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := api.Exec(context.Background(), `query($id:ID!){ approach(id:$id){title display} }`, "", map[string]any{"id": a.ID})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Approach struct{ Title, Display string }
		}
		if err = json.Unmarshal(bytes, &got); err != nil {
			t.Fatal(err)
		}
		if got.Approach.Title != raw || got.Approach.Display != plan.DisplayStructured(raw) || strings.Contains(got.Approach.Display, "\x1b") {
			t.Fatalf("raw/display contract %s", bytes)
		}
		// display-only selection must still fetch and budget the owning title.
		bytes, err = api.Exec(context.Background(), `query($id:ID!){ approach(id:$id){display} }`, "", map[string]any{"id": a.ID})
		if err != nil {
			t.Fatal(err)
		}
		got.Approach.Display = ""
		json.Unmarshal(bytes, &got)
		if got.Approach.Display != plan.DisplayStructured(raw) {
			t.Fatalf("display-only missing: %s", bytes)
		}
		stored, err := plan.GetApproach(s, a.ID)
		if err != nil || stored.Title != raw {
			t.Fatal("presentation mutated stored contract")
		}
	}
}
