package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func noticeApproach(t *testing.T, a *API, s *store.Store) string {
	t.Helper()
	p := projectExec(t, a, `mutation { createPlan(title:"Cache repair",goal:"Correct cache behavior") {id} }`)
	app, err := plan.CreateApproach(s, p.Effects[0].ID, "Inspect and repair")
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func TestNoticedPersistsAndSeparatesApproaches(t *testing.T) {
	db, root := filepath.Join(t.TempDir(), "db"), t.TempDir()
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	a := projectAPI(t, s, root)
	app := noticeApproach(t, a, s)
	other := noticeApproach(t, a, s)
	q := fmt.Sprintf(`mutation {
 first:noticed(approachId:%q,body:"Cache may grow without bound; not measured yet.",filePath:"cache.go"){id key body filePath sessionRef}
 second:noticed(approachId:%q,body:"Two tests repeat the same setup."){id}
 unrelated:noticed(approachId:%q,body:"Separate approach observation."){id}
 }`, app, app, other)
	r := projectExec(t, a, q)
	if len(r.Effects) != 3 || r.Effects[0].ID == r.Effects[1].ID || !strings.Contains(string(r.Data), "real-session") {
		t.Fatalf("independent durable receipts: %+v", r)
	}
	for _, effect := range r.Effects {
		if effect.Status != "applied" || effect.Field != "noticed" {
			t.Fatalf("wrong receipt: %+v", effect)
		}
	}
	before, err := plan.GetApproach(s, app)
	if err != nil || before.State != plan.ApproachDraft {
		t.Fatalf("notice changed workflow: %+v %v", before, err)
	}
	if tasks, err := plan.ApproachTasks(s, app); err != nil || len(tasks) != 0 {
		t.Fatalf("notice created work: %+v %v", tasks, err)
	}
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a = projectAPI(t, s, root)
	got := projectExec(t, a, fmt.Sprintf(`{notices(approachId:%q){id key body filePath sessionRef}}`, app))
	var data struct {
		Notices []struct{ ID, Key, Body, FilePath, SessionRef string }
	}
	if err := json.Unmarshal(got.Data, &data); err != nil || len(data.Notices) != 2 {
		t.Fatalf("reopen scoped recall: %s %v", got.Data, err)
	}
	for _, note := range data.Notices {
		if !strings.HasPrefix(note.Key, "noticed/"+app+"/") || note.SessionRef != "real-session" {
			t.Fatalf("association/provenance: %+v", note)
		}
	}
	// Existing version updates remain available, with no special notice ledger.
	note := data.Notices[0]
	projectExec(t, a, fmt.Sprintf(`mutation{appendKnowledge(key:%q,body:"Measured later; observation clarified.",expectedPreviousId:%q){id}}`, note.Key, note.ID))
	got = projectExec(t, a, fmt.Sprintf(`{notices(approachId:%q){body}}`, app))
	if !strings.Contains(string(got.Data), "observation clarified") || strings.Contains(string(got.Data), note.Body) {
		t.Fatalf("recall must return current versions: %s", got.Data)
	}
}

func TestNoticedRefusalsAndPartialReceipts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	app := noticeApproach(t, a, s)
	foreign := projectAPI(t, s, t.TempDir())
	foreignApp := noticeApproach(t, foreign, s)
	limits := config.Defaults().Tools.GraphQL
	for _, q := range []string{
		fmt.Sprintf(`mutation{noticed(approachId:%q,body:"  "){id}}`, app),
		`mutation{noticed(approachId:"missing",body:"bad link"){id}}`,
		fmt.Sprintf(`mutation{noticed(approachId:%q,body:"foreign"){id}}`, foreignApp),
		fmt.Sprintf(`{notices(approachId:%q){body}}`, foreignApp),
		fmt.Sprintf(`mutation{noticed(approachId:%q,body:"outside file",filePath:"../outside.go"){id}}`, app),
		fmt.Sprintf(`mutation{noticed(approachId:%q,body:"invalid selection"){invented}}`, app),
	} {
		if b, err := a.ExecBounded(context.Background(), q, "", nil, limits); err == nil {
			t.Fatalf("invalid call accepted: %s %s", q, b)
		}
	}
	if got := projectExec(t, a, fmt.Sprintf(`{notices(approachId:%q){id}}`, app)); string(got.Data) != `{"notices":[]}` {
		t.Fatalf("refusal wrote a note: %s", got.Data)
	}
	q := fmt.Sprintf(`mutation{saved:noticed(approachId:%q,body:"actual observation"){id} bad:noticed(approachId:"missing",body:"invalid"){id}}`, app)
	b, err := a.ExecBounded(context.Background(), q, "", nil, limits)
	var result Result
	if err == nil || json.Unmarshal(b, &result) != nil || len(result.Effects) != 2 || result.Effects[0].Status != "applied" || result.Effects[0].ID == "" || result.Effects[1].Status != "rolled_back" {
		t.Fatalf("partial-effect receipt lost: %s %v", b, err)
	}
	got := projectExec(t, a, fmt.Sprintf(`{knowledge(id:%q){body}}`, result.Effects[0].ID))
	if !strings.Contains(string(got.Data), "actual observation") {
		t.Fatalf("committed receipt not recallable: %s", got.Data)
	}
}
