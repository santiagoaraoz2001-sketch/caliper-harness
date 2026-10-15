package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

func TestGraphCoreOriginalPrefetch(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
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
	task, err := plan.CreateTask(s, a.ID, "t", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.SetTaskResult(s, task.ID, strings.Repeat("界", 1024*1024)); err != nil {
		t.Fatal(err)
	}
	api := New(s, "test", t.TempDir())
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out, err := api.Exec(context.Background(), fmt.Sprintf(`{ task(id:%q) { id } }`, task.ID), "", nil)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("id-only bytes=%d allocated=%d", len(out), after.TotalAlloc-before.TotalAlloc)
	if after.TotalAlloc-before.TotalAlloc > 1024*1024 {
		t.Fatalf("id-only query materialized unselected large source: %d", after.TotalAlloc-before.TotalAlloc)
	}
}

func TestGraphCoreKuzuLengthUnits(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Query(`RETURN size($s) AS n`, map[string]any{"s": "A界🙂"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native size(A界🙂)=%v utf8Bytes=%d", rows, len("A界🙂"))
	if fmt.Sprint(rows[0]["n"]) != "3" {
		t.Fatalf("unconfirmed Unicode scalar admission units: %v", rows)
	}
}

func TestGraphCoreInputBoundary(t *testing.T) {
	values := []any{nil, "", true, int64(123), map[string]any{}, []any{}, map[string]any{"empty": []any{}, "s": "\t\n界<&\u2028"}, []any{false, nil, "bad\xff", map[string]any{"a": float64(1.25)}}}
	for _, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		n, err := inputSize(v, len(raw))
		if err != nil || n != len(raw) {
			t.Fatalf("exact boundary raw=%s n=%d err=%v", raw, n, err)
		}
		if _, err = inputSize(v, len(raw)-1); err == nil {
			t.Fatalf("+1 accepted: %s", raw)
		}
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := New(s, "v", t.TempDir())
	q := `query($id:ID!){session(id:$id){id}}`
	vars := map[string]any{"id": "missing"}
	v, _ := json.Marshal(vars)
	g := config.Defaults().Tools.GraphQL
	g.MaxRequestBytes = len(q) + len(v)
	if _, err := api.ExecBounded(context.Background(), q, "", vars, g); err != nil {
		t.Fatal(err)
	}
	g.MaxRequestBytes--
	if _, err := api.ExecBounded(context.Background(), q, "", vars, g); err == nil {
		t.Fatal("oversized request accepted")
	}
}
func TestGraphCoreSessionExistence(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	api := New(s, "v", dir)
	out, err := api.Exec(context.Background(), `{session(id:"missing"){id}}`, "", nil)
	if err != nil || string(out) != `{"session":null}` {
		t.Fatalf("missing fabricated: %s %v", out, err)
	}
	if _, err = api.Exec(context.Background(), `{session(id:"../outside"){id}}`, "", nil); err == nil {
		t.Fatal("unsafe session id accepted")
	}
	if err = session.Append(dir, "existing", session.Event{Kind: "raw", Text: strings.Repeat("x", 256*1024)}); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	out, err = api.Exec(context.Background(), `{session(id:"existing"){id}}`, "", nil)
	runtime.ReadMemStats(&b)
	if err != nil || !strings.Contains(string(out), "existing") || b.TotalAlloc-a.TotalAlloc > 65536 {
		t.Fatalf("id-only read materialized log: alloc=%d out=%s err=%v", b.TotalAlloc-a.TotalAlloc, out, err)
	}
}
func TestGraphCoreEssentialSessionReceipt(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	api := New(s, "v", dir)
	id := strings.Repeat("<&", 60)
	q := `mutation($id:ID!,$body:String!){a:addSessionEvent(sessionId:$id,kind:"event",text:$body){text} b:createPlan(title:"",goal:"bad"){id}}`
	vars := map[string]any{"id": id, "body": strings.Repeat("large ", 2048)}
	g := config.Defaults().Tools.GraphQL
	g.MaxResultBytes = 600
	if _, err = api.ExecBounded(context.Background(), q, "", vars, g); err == nil {
		t.Fatal("receipt overflow admitted")
	}
	if _, err = os.Stat(filepath.Join(dir, id+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("effect before receipt admission: %v", err)
	}
	g.MaxResultBytes = 2400
	out, err := api.ExecBounded(context.Background(), q, "", vars, g)
	if err == nil {
		t.Fatal("failed later owner/output hidden")
	}
	var result Result
	if e := json.Unmarshal(out, &result); e != nil {
		t.Fatalf("broken envelope: %s %v", out, e)
	}
	if len(result.Effects) != 2 || result.Effects[0].Status != "applied" || result.Effects[0].ID != id || result.Effects[1].Status != "unknown" {
		t.Fatalf("committed id lost: %s", out)
	}
	events, e := session.Read(dir, id)
	if e != nil || len(events) != 1 || events[0].Text != vars["body"] {
		t.Fatalf("actual append missing: %v %v", events, e)
	}
	if len(out) > g.MaxResultBytes {
		t.Fatal("result overflow")
	}
	// Without the later owner error, selected large text independently overflows
	// the encoded result. The applied arbitrary ID must still survive omission.
	vars["id"] = id + "-output"
	q = `mutation($id:ID!,$body:String!){a:addSessionEvent(sessionId:$id,kind:"event",text:$body){text}}`
	out, err = api.ExecBounded(context.Background(), q, "", vars, g)
	if e := json.Unmarshal(out, &result); e != nil || err == nil || !result.DataOmitted || len(result.Effects) != 1 || result.Effects[0].Status != "applied" || result.Effects[0].ID != vars["id"] || len(out) > g.MaxResultBytes {
		t.Fatalf("output omission lost actual applied id: %s err=%v decode=%v", out, err, e)
	}

}
func TestGraphCoreIntrospectionBudgets(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := New(s, "v", t.TempDir())
	g := config.Defaults().Tools.GraphQL
	q := `{ a:__schema { types { ...N } } b:__type(name:"Task") { fields { name } } } fragment N on __Type { first:name second:name }`
	out, err := api.ExecBounded(context.Background(), q, "", nil, g)
	if err != nil {
		t.Fatalf("valid introspection: %s %v", out, err)
	}
	g.MaxRows = 3
	if _, err = api.ExecBounded(context.Background(), q, "", nil, g); err == nil || !strings.Contains(err.Error(), "max_rows") {
		t.Fatalf("introspection records bypass budget: %v", err)
	}
	g = config.Defaults().Tools.GraphQL
	g.MaxResultBytes = 220
	if _, err = api.ExecBounded(context.Background(), q, "", nil, g); err == nil {
		t.Fatal("introspection scalar budget bypassed")
	}
}

func TestGraphCoreComposedEffectsAndTypedAdmission(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := New(s, "v", t.TempDir())
	g := config.Defaults().Tools.GraphQL
	invalid := `mutation {a:createPlan(title:"must not exist",goal:"g"){id} b:createPlan(title:7,goal:"g"){id}}`
	if out, err := api.ExecBounded(context.Background(), invalid, "", nil, g); err == nil {
		t.Fatalf("invalid whole operation admitted: %s", out)
	}
	if rows, err := s.Query(`MATCH(p:Plan) RETURN p.id AS id`, nil); err != nil || len(rows) != 0 {
		t.Fatalf("effect before whole-op validation: %v %v", rows, err)
	}
	q := `mutation {a:createPlan(title:"A",goal:"g"){id} b:createPlan(title:"",goal:"bad"){id} c:addMemory(kind:"note",body:"C after owner refusal",sessionRef:""){id}}`
	out, err := api.ExecBounded(context.Background(), q, "", nil, g)
	var result Result
	if e := json.Unmarshal(out, &result); e != nil || err == nil || len(result.Effects) != 3 || result.Effects[0].Status != "applied" || result.Effects[1].Status != "unknown" || result.Effects[2].Status != "applied" {
		t.Fatalf("partial effects hidden: %s %v %v", out, err, e)
	}
	for _, index := range []int{0, 2} {
		if result.Effects[index].ID == "" {
			t.Fatalf("applied id missing: %s", out)
		}
	}
	plans, err := s.Query(`MATCH(p:Plan) RETURN p.id AS id,p.title AS title`, nil)
	memories, e := s.Query(`MATCH(m:MemoryEntry) RETURN m.id AS id,m.body AS body`, nil)
	if err != nil || e != nil || len(plans) != 1 || plans[0]["id"] != result.Effects[0].ID || plans[0]["title"] != "A" || len(memories) != 1 || memories[0]["id"] != result.Effects[2].ID || memories[0]["body"] != "C after owner refusal" {
		t.Fatalf("receipts do not match real commits: %v %v %v %v", plans, memories, err, e)
	}
	g.MaxSelections = 1
	if _, err := api.ExecBounded(context.Background(), `mutation{createPlan(title:"not admitted",goal:"g"){id}}`, "", nil, g); err == nil {
		t.Fatal("expanded selection bound bypassed")
	}
	plans, _ = s.Query(`MATCH(p:Plan) RETURN p.id AS id`, nil)
	if len(plans) != 1 {
		t.Fatalf("selection admission had effects: %v", plans)
	}
	g = config.Defaults().Tools.GraphQL
	g.MaxRows = 1
	q = fmt.Sprintf(`{a:plan(id:%q){...P} b:plan(id:%q){...P}} fragment P on Plan{id}`, result.Effects[0].ID, result.Effects[0].ID)
	if _, err := api.ExecBounded(context.Background(), q, "", nil, g); err == nil || !strings.Contains(err.Error(), "max_rows") {
		t.Fatalf("aggregate aliases bypassed row bound: %v", err)
	}
}

func TestGraphCoreSelectedSourceAndResultBoundary(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := plan.CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "t", "p")
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.SetTaskResult(s, task.ID, strings.Repeat("界", 1024*1024)); err != nil {
		t.Fatal(err)
	}
	api := New(s, strings.Repeat("v", 512), t.TempDir())
	g := config.Defaults().Tools.GraphQL
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out, err := api.ExecBounded(context.Background(), fmt.Sprintf(`{task(id:%q){result}}`, task.ID), "", nil, g)
	runtime.ReadMemStats(&after)
	t.Logf("selected huge source refused: alloc=%d bytes=%d err=%v", after.TotalAlloc-before.TotalAlloc, len(out), err)
	if err == nil || !strings.Contains(err.Error(), "source") || after.TotalAlloc-before.TotalAlloc > 1024*1024 {
		t.Fatalf("selected large source crossed before refusal: %s %v alloc=%d", out, err, after.TotalAlloc-before.TotalAlloc)
	}
	out, err = api.ExecBounded(context.Background(), `{version}`, "", nil, g)
	if err != nil {
		t.Fatal(err)
	}
	exact := append([]byte(nil), out...)
	g.MaxResultBytes = len(exact)
	out, err = api.ExecBounded(context.Background(), `{version}`, "", nil, g)
	if err != nil || string(out) != string(exact) {
		t.Fatalf("exact result boundary refused: %s %v", out, err)
	}
	g.MaxResultBytes--
	out, err = api.ExecBounded(context.Background(), `{version}`, "", nil, g)
	var result Result
	if e := json.Unmarshal(out, &result); e != nil || err == nil || !result.DataOmitted || len(out) > g.MaxResultBytes {
		t.Fatalf("overflow not explicit bounded JSON: %s %v %v", out, err, e)
	}
}

func TestGraphCoreNumericAndLegacyReceiptIdentity(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	api := New(s, "v", dir)
	g := config.Defaults().Tools.GraphQL
	numeric := "1234567890123456789"
	q := `mutation{addSessionEvent(sessionId:1234567890123456789,kind:"note",text:"numeric effect"){text}}`
	g.MaxResultBytes = 198
	out, err := api.ExecBounded(context.Background(), q, "", nil, g)
	if err == nil {
		t.Fatalf("essential receipt overflow admitted: %s", out)
	}
	if _, e := os.Stat(filepath.Join(dir, numeric+".jsonl")); !os.IsNotExist(e) {
		t.Fatalf("effect before actual ID reservation: %v", e)
	}
	g = config.Defaults().Tools.GraphQL
	out, err = api.ExecBounded(context.Background(), q, "", nil, g)
	var result Result
	if e := json.Unmarshal(out, &result); e != nil || err != nil || len(result.Effects) != 1 || result.Effects[0].ID != numeric || result.Effects[0].Status != "applied" {
		t.Fatalf("numeric ID not coerced consistently: %s %v %v", out, err, e)
	}
	events, e := session.Read(dir, numeric)
	if e != nil || len(events) != 1 || events[0].Text != "numeric effect" {
		t.Fatalf("normal numeric append: %v %v", events, e)
	}
	// Legacy store entries can have actual IDs longer than generated UUIDs. The
	// existing owning transition accepts them; the receipt must reserve that ID.
	id := strings.Repeat("<&", 80)
	if err = s.Write(`CREATE(p:Plan{id:$id,title:'legacy',goal:'g',state:'draft',created_at:$now})`, map[string]any{"id": id, "now": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	q = fmt.Sprintf(`mutation{transitionPlan(id:%q,to:"active"){id state}}`, id)
	g.MaxResultBytes = 180
	if out, err = api.ExecBounded(context.Background(), q, "", nil, g); err == nil {
		t.Fatalf("legacy receipt overflow admitted: %s", out)
	}
	p, err := plan.GetPlan(s, id)
	if err != nil || p.State != plan.PlanDraft {
		t.Fatalf("unreserved legacy transition had effects: %v %v", p, err)
	}
	g.MaxResultBytes = 2000
	out, err = api.ExecBounded(context.Background(), q, "", nil, g)
	if e = json.Unmarshal(out, &result); e != nil || err == nil || !result.DataOmitted || len(result.Effects) != 1 || result.Effects[0].Status != "applied" || result.Effects[0].ID != id || len(out) > g.MaxResultBytes {
		t.Fatalf("legacy applied ID lost on output overflow: %s %v %v", out, err, e)
	}
	p, err = plan.GetPlan(s, id)
	if err != nil || p.State != plan.PlanActive {
		t.Fatalf("receipt not actual transition: %v %v", p, err)
	}
}
