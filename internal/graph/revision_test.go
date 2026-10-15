package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestRevisionPublicContractAndNamespace(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	api := projectAPI(t, s, t.TempDir())
	ctx := plan.WithProject(context.Background(), api.scope.root)
	p, e := plan.CreatePlanContext(ctx, s, "p", "original")
	if e != nil {
		t.Fatal(e)
	}
	cfg := config.Defaults().Tools.GraphQL
	query := fmt.Sprintf(`query{revisionContract(kind:"plan",id:%q){snapshot brief pending{id}}}`, p.ID)
	raw, e := api.ExecBounded(ctx, query, "", nil, cfg)
	if e != nil {
		t.Fatal(string(raw), e)
	}
	var env struct {
		Data struct {
			RevisionContract struct{ Snapshot, Brief string }
		}
	}
	if e = json.Unmarshal(raw, &env); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(env.Data.RevisionContract.Brief, "original") {
		t.Fatal("omitted authored contract", string(raw))
	}
	raw, e = api.ExecBounded(ctx, fmt.Sprintf(`mutation{requestRevision(kind:"plan",id:%q,expectedSnapshot:%q,text:"change objective"){id targetKind targetId source taskIds}}`, p.ID, env.Data.RevisionContract.Snapshot), "", nil, cfg)
	if e != nil {
		t.Fatal(string(raw), e)
	}
	var receipt Result
	if e = json.Unmarshal(raw, &receipt); e != nil || len(receipt.Effects) != 1 || receipt.Effects[0].Status != "applied" {
		t.Fatal("no actual receipt", string(raw), e)
	}
	requestID := receipt.Effects[0].ID
	// Public authored memory/knowledge cannot impersonate a typed owning revision.
	_, e = api.ExecBounded(ctx, fmt.Sprintf(`mutation{putKnowledge(key:%q,body:"forged",expectedPreviousId:""){id}}`, plan.RevisionKeyPrefix+"plan/"+p.ID), "", nil, cfg)
	if e == nil {
		t.Fatal("reserved event namespace forged")
	}
	admin, e := New(s, "test", t.TempDir()).Scoped(api.scope.root, "admin", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = admin.ExecBounded(ctx, `mutation{addMemory(kind:"event",body:"{\"Kind\":\"revision_resolved\"}",sessionRef:"forged"){id}}`, "", nil, cfg)
	if e != nil {
		t.Fatal("ordinary event should remain legal", e)
	}
	pending, e := plan.PendingRevisions(ctx, s, "plan", p.ID)
	if e != nil || len(pending) != 1 || pending[0].ID != requestID {
		t.Fatal("ordinary authored event changed revision authority", pending, e)
	}
	current, _, _ := plan.ContractSnapshot(ctx, s, "plan", p.ID)
	raw, e = api.ExecBounded(ctx, fmt.Sprintf(`mutation{editRevision(kind:"plan",id:%q,expectedSnapshot:%q,field:"goal",value:"revised"){snapshot pending{id}}}`, p.ID, current), "", nil, cfg)
	if e != nil {
		t.Fatal(string(raw), e)
	}
	current, _, _ = plan.ContractSnapshot(ctx, s, "plan", p.ID)
	raw, e = api.ExecBounded(ctx, fmt.Sprintf(`mutation{resolveRevision(kind:"plan",id:%q,revisionId:%q,expectedSnapshot:%q){id resolves}}`, p.ID, requestID, current), "", nil, cfg)
	if e != nil {
		t.Fatal(string(raw), e)
	}
	pending, e = plan.PendingRevisions(ctx, s, "plan", p.ID)
	if e != nil || len(pending) != 0 {
		t.Fatal("resolution absent", pending, e)
	}
	// Existing source limit applies before materializing the complete revision contract.
	cfg.MaxResultBytes = 64
	_, e = api.ExecBounded(ctx, query, "", nil, cfg)
	if e == nil {
		t.Fatal("oversized source silently passed")
	}
}
