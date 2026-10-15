package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestTransactionRefusalPreservesCommittedSibling(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	created := projectExec(t, a, `mutation{createPlan(title:"audit",goal:"truthful effect receipts"){id state}}`)
	id := created.Effects[0].ID
	q := fmt.Sprintf(`mutation{saved:appendKnowledge(key:"audit",body:"actual committed metadata",expectedPreviousId:""){id} invalid:transitionPlan(id:%q,to:"ready"){id state}}`, id)
	raw, e := a.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL)
	var r Result
	if e == nil || json.Unmarshal(raw, &r) != nil {
		t.Fatalf("expected actual owner refusal: %s %v", raw, e)
	}
	plan := projectExec(t, a, fmt.Sprintf(`{plan(id:%q){id state}}`, id))
	head := projectExec(t, a, `{knowledge(key:"audit"){id body}}`)
	if !strings.Contains(string(plan.Data), `"state":"draft"`) || !strings.Contains(string(head.Data), "actual committed metadata") || len(r.Effects) != 2 || r.Effects[0].Status != "applied" {
		t.Fatalf("repro instrument failed: receipt=%s plan=%s head=%s", raw, plan.Data, head.Data)
	}
	t.Logf("actual owner refusal=%v; receipt=%s; exact owning plan=%s; committed metadata=%s", e, raw, plan.Data, head.Data)
	if r.Effects[1].Status != "rolled_back" {
		t.Fatal("confirmed rollback receipt missing for rejected transition")
	}
}

// A readiness hook may settle several independent operations. Its last
// transaction's rollback is not proof that the whole hook had no effects.
func TestTransactionFactDoesNotOverclaimReadinessHook(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "before", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("later owner validation refused")
	called := false
	api := NewWithReady(s, "test", t.TempDir(), func(ctx context.Context, id string) (*plan.Approach, error) {
		called = true
		if err := s.WriteContext(ctx, `MATCH(p:Plan) WHERE p.id=$id SET p.title='earlier committed effect'`, map[string]any{"id": p.ID}); err != nil {
			return nil, err
		}
		return nil, s.UpdateContext(ctx, func(*store.Tx) error { return refused })
	})
	raw, err := api.ExecBounded(context.Background(), fmt.Sprintf(`mutation{transitionApproach(id:%q,to:"ready"){id}}`, a.ID), "", nil, config.Defaults().Tools.GraphQL)
	var result Result
	if !called || err == nil || json.Unmarshal(raw, &result) != nil || len(result.Effects) != 1 || result.Effects[0].Status != "unknown" {
		t.Fatalf("hook effect overclaimed: %s %v called=%v", raw, err, called)
	}
	actual, err := plan.GetPlan(s, p.ID)
	if err != nil || actual.Title != "earlier committed effect" {
		t.Fatalf("actual committed hook effect: %+v %v", actual, err)
	}
}
