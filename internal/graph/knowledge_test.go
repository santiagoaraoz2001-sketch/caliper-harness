package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"strings"
	"sync"
	"testing"
)

func projectAPI(t *testing.T, s *store.Store, root string) *API {
	t.Helper()
	// These graph ownership/projection fixtures explicitly supply a tokenizer.
	// Domain boundary and native tokenizer behavior have separate tests.
	a, e := New(s, "test", t.TempDir()).WithDomainTokenCounter(func(context.Context, string) (int, error) { return 100, nil }).Scoped(root, "real-session", true)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func projectExec(t *testing.T, a *API, q string) Result {
	t.Helper()
	b, e := a.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL)
	if e != nil {
		t.Fatalf("%s: %s %v", q, b, e)
	}
	var r Result
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestKnowledgeScopedCASAndReopen(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	s, e := store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	a := projectAPI(t, s, root)
	root = a.scope.root
	r := projectExec(t, a, `mutation { putKnowledge(key:"design/colors",body:"blue α🙂\n",expectedPreviousId:"") { id body sessionRef } }`)
	if len(r.Effects) != 1 || r.Effects[0].Status != "applied" {
		t.Fatalf("actual receipt %s", r.Data)
	}
	first := r.Effects[0].ID
	body := `{ knowledge(key:"design/colors",limit:1) { id body sessionRef } }`
	got := projectExec(t, a, body)
	if !strings.Contains(string(got.Data), `blue α🙂\n`) || !strings.Contains(string(got.Data), "real-session") {
		t.Fatal(string(got.Data))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := memory.PutKnowledge(context.Background(), s, root, "real-session", "design/colors", "green", first, "", "")
			if e == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("CAS winners %d", successes)
	}
	historical := projectExec(t, a, fmt.Sprintf(`{ knowledge(id:%q) { id body previousId } }`, first))
	if !strings.Contains(string(historical.Data), "blue α") {
		t.Fatal(string(historical.Data))
	}
	legacy, e := memory.Add(s, "note", "global", "")
	if e != nil {
		t.Fatal(e)
	}
	entries, e := memory.List(s, "", "", 0)
	if e != nil || len(entries) != 1 || entries[0].ID != legacy.ID {
		t.Fatalf("global leak %+v %v", entries, e)
	}
	foreign := projectAPI(t, s, t.TempDir())
	q := projectExec(t, foreign, body)
	if string(q.Data) != `{"knowledge":[]}` {
		t.Fatalf("foreign read %s", q.Data)
	}
	s.Close()
	s, e = store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	reopened := projectExec(t, projectAPI(t, s, root), body)
	if !strings.Contains(string(reopened.Data), "green") {
		t.Fatal(string(reopened.Data))
	}
}
func TestKnowledgeProjectPATAndTrust(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	a := projectAPI(t, s, root)
	root = a.scope.root
	r := projectExec(t, a, `mutation { createPlan(title:"p",goal:"g") { id projectRoot } }`)
	id := r.Effects[0].ID
	app, e := plan.CreateApproach(s, id, "a")
	if e != nil {
		t.Fatal(e)
	}
	task, e := plan.CreateTask(s, app.ID, "t", "p")
	if e != nil {
		t.Fatal(e)
	}
	own := projectExec(t, a, `{ plans { id approaches { id tasks { id } } } }`)
	if !strings.Contains(string(own.Data), task.ID) {
		t.Fatal(string(own.Data))
	}
	other := projectAPI(t, s, t.TempDir())
	wrong := projectExec(t, other, fmt.Sprintf(`{ plan(id:%q) { id } approach(id:%q) { id } task(id:%q) { id } plans { id } }`, id, app.ID, task.ID))
	if strings.Contains(string(wrong.Data), id) || strings.Contains(string(wrong.Data), task.ID) {
		t.Fatal(string(wrong.Data))
	}
	for _, q := range []string{fmt.Sprintf(`mutation { transitionTask(id:%q,to:"queued") { id } }`, task.ID), fmt.Sprintf(`mutation { putKnowledge(key:"bad",body:"b",expectedPreviousId:"",taskId:%q) { id } }`, task.ID)} {
		if b, e := other.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL); e == nil {
			t.Fatalf("foreign mutation %s", b)
		}
	}
	for _, q := range []string{`mutation { a:createPlan(title:"never",goal:"never") {id} ...Host } fragment Host on Mutation { hidden:addSessionEvent(sessionId:"forged",kind:"coding",text:"PASS") { kind } }`, `mutation { addMemory(kind:"note",body:"spoof",sessionRef:"fake") {id} }`, `{ sessions {id} }`, `{ session(id:"other") {id} }`} {
		if _, e := a.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL); e == nil {
			t.Fatalf("host bridge admitted %s", q)
		}
	}
	plans, e := plan.ListPlans(s, "")
	if e != nil || len(plans) != 1 {
		t.Fatalf("earlier effect escaped whole trust admission: %d %v", len(plans), e)
	}
	legacy, e := plan.CreatePlan(s, "legacy", "goal")
	if e != nil {
		t.Fatal(e)
	}
	projectExec(t, a, fmt.Sprintf(`mutation { bindPlanProject(id:%q) { id projectRoot } }`, legacy.ID))
	if _, e := other.ExecBounded(context.Background(), fmt.Sprintf(`mutation { bindPlanProject(id:%q) {id} }`, legacy.ID), "", nil, config.Defaults().Tools.GraphQL); e == nil {
		t.Fatal("foreign rebind")
	}
}
func TestKnowledgePartialReceiptsAndPrefetch(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	a := projectAPI(t, s, root)
	root = a.scope.root
	cfg := config.Defaults().Tools.GraphQL
	q := `mutation { a:putKnowledge(key:"a",body:"one",expectedPreviousId:"") {id} b:putKnowledge(key:"a",body:"bad",expectedPreviousId:"") {id} c:putKnowledge(key:"c",body:"three",expectedPreviousId:"") {id} }`
	b, e := a.ExecBounded(context.Background(), q, "", nil, cfg)
	if e == nil {
		t.Fatal("stale CAS accepted")
	}
	var r Result
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	if len(r.Effects) != 3 || r.Effects[0].Status != "applied" || r.Effects[2].Status != "applied" || r.Effects[0].ID == "" || r.Effects[2].ID == "" {
		t.Fatalf("partial %s", b)
	}
	heads, e := memory.Knowledge(context.Background(), s, root, "", "", 10, 65536, true)
	if e != nil || len(heads) != 2 {
		t.Fatalf("actual commits %d %v", len(heads), e)
	}
	_, e = memory.PutKnowledge(context.Background(), s, root, "actual", "huge", strings.Repeat("界", 1024*1024), "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	projectExec(t, a, `{ knowledge(key:"huge",limit:1) {id} }`)
	if b, e = a.ExecBounded(context.Background(), `{ knowledge(key:"huge",limit:1) {body} }`, "", nil, cfg); e == nil || !strings.Contains(string(b), "source") {
		t.Fatalf("unbounded source %s %v", b, e)
	}
	cfg.MaxResultBytes = 200
	q = `mutation { z:putKnowledge(key:"noeffect",body:"x",expectedPreviousId:"") {id} }`
	if _, e = a.ExecBounded(context.Background(), q, "", nil, cfg); e == nil {
		t.Fatal("tiny receipt admitted")
	}
	heads, e = memory.Knowledge(context.Background(), s, root, "noeffect", "", 1, 65536, true)
	if e != nil || len(heads) != 0 {
		t.Fatal("receipt reservation had effect")
	}
}

func TestKnowledgeEffectSurvivesSelectedFailureAndLinkRefusal(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	cfg := config.Defaults().Tools.GraphQL
	cfg.MaxResultBytes = 700
	query := fmt.Sprintf(`mutation { putKnowledge(key:"committed",body:%q,expectedPreviousId:"") { id body } }`, strings.Repeat("x", 1000))
	b, e := a.ExecBounded(context.Background(), query, "", nil, cfg)
	if e == nil {
		t.Fatal("selected oversized data accepted")
	}
	var r Result
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatalf("receipt lost %q %v", b, e)
	}
	if len(r.Effects) != 1 || r.Effects[0].Status != "applied" || r.Effects[0].ID == "" {
		t.Fatalf("actual commit lost %s", b)
	}
	notes, e := memory.Knowledge(context.Background(), s, a.scope.root, "", r.Effects[0].ID, 1, 65536, true)
	if e != nil || len(notes) != 1 || notes[0].Body != strings.Repeat("x", 1000) {
		t.Fatalf("actual persisted effect %v %v", notes, e)
	}
	for _, file := range []string{filepath.Join(t.TempDir(), "outside"), "../outside"} {
		q := fmt.Sprintf(`mutation {putKnowledge(key:"no-link",body:"x",expectedPreviousId:"",filePath:%q){id}}`, file)
		if _, e = a.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL); e == nil {
			t.Fatal("outside link admitted")
		}
	}
	if _, e = a.ExecBounded(context.Background(), `mutation {putKnowledge(key:"no-typed",body:"x",expectedPreviousId:""){id nonexistent}}`, "", nil, config.Defaults().Tools.GraphQL); e == nil {
		t.Fatal("bad typed selection admitted")
	}
	notes, e = memory.Knowledge(context.Background(), s, a.scope.root, "no-typed", "", 1, 65536, true)
	if e != nil || len(notes) != 0 {
		t.Fatal("bad typed operation wrote")
	}
}
