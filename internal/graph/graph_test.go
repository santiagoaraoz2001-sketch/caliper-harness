package graph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// The round-trip runs entirely in-process: this suite passing inside the
// socket-blocked sandbox proves zero network, since any dial would fail.
func TestGraphRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())
	ctx := context.Background()

	out, err := api.Exec(ctx, `mutation {
		addMemory(kind: "note", body: "hello-graph", sessionRef: "s1") { id kind body sessionRef }
	}`, "", nil)
	if err != nil {
		t.Fatalf("mutation: %s", err)
	}
	var added struct {
		AddMemory struct {
			ID, Kind, Body, SessionRef string
		}
	}
	if err := json.Unmarshal(out, &added); err != nil {
		t.Fatalf("decode mutation: %s", err)
	}
	if added.AddMemory.Body != "hello-graph" || added.AddMemory.Kind != "note" ||
		added.AddMemory.SessionRef != "s1" || added.AddMemory.ID == "" {
		t.Fatalf("wrong mutation result: %s", out)
	}

	out, err = api.Exec(ctx, `{ version memories { id body } }`, "", nil)
	if err != nil {
		t.Fatalf("query: %s", err)
	}
	var got struct {
		Version  string
		Memories []struct{ ID, Body string }
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode query: %s", err)
	}
	if got.Version != "test-version" {
		t.Fatalf("wrong version: %s", out)
	}
	if len(got.Memories) != 1 || got.Memories[0].ID != added.AddMemory.ID ||
		got.Memories[0].Body != "hello-graph" {
		t.Fatalf("wrong query result: %s", out)
	}
}

const prepareDocument = `mutation($input:FreshTaskInput!) { fresh:prepareTask(input:$input) { plan{id state} approach{id state} task{id state current target sketch acceptance tests verify checkCommand acceptanceCommand} } }`

func freshTaskValues() map[string]any {
	return map[string]any{"planTitle": "requested repair", "planGoal": "actual requested behavior", "approachTitle": "one supported strategy", "taskTitle": "focused repair", "taskPrompt": "perform inspected repair", "current": "observed wrong result κ", "target": "requested corrected result", "sketch": "small inspected change", "acceptance": "actual project check exit0", "tests": "existing project scope", "verify": "independent actual check and inspection", "checkCommand": "true", "acceptanceCommand": "test ! -e forbidden-effect"}
}

func TestPrepareTaskPublicCreation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, "test", t.TempDir()).Scoped(t.TempDir(), "preparation", true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := api.ExecBounded(context.Background(), prepareDocument, "", map[string]any{"input": freshTaskValues()}, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatalf("public preparation unavailable: %s %v", out, err)
	}
	var receipt Result
	if err = json.Unmarshal(out, &receipt); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Fresh struct {
			Plan, Approach, Task struct{ ID, State string }
		}
	}
	if err = json.Unmarshal(receipt.Data, &data); err != nil {
		t.Fatal(err)
	}
	f := data.Fresh
	if f.Plan.ID == "" || f.Approach.ID == "" || f.Task.ID == "" || f.Plan.State != plan.PlanDraft || f.Approach.State != plan.ApproachDraft || f.Task.State != plan.TaskQueued {
		t.Fatalf("false preparation identity/state: %s", out)
	}
	actual, err := plan.GetTask(s, f.Task.ID)
	parent, pe := plan.TaskApproach(s, f.Task.ID)
	owner, oe := plan.ApproachPlan(s, f.Approach.ID)
	if err != nil || pe != nil || oe != nil || parent.ID != f.Approach.ID || owner.ID != f.Plan.ID || owner.ProjectRoot != api.CapturedProject() || actual.Current != freshTaskValues()["current"] || actual.CheckCommand != "true" || actual.Attempts != 0 || actual.Qualified() {
		t.Fatalf("owning preparation differs: task=%+v parent=%+v owner=%+v errors=%v/%v/%v", actual, parent, owner, err, pe, oe)
	}
	if len(receipt.Effects) != 4 {
		t.Fatalf("missing per-step receipts: %s", out)
	}
	t.Logf("actual public receipt: %s", out)
}

func TestGraphMemoriesFilters(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())
	ctx := context.Background()

	for _, m := range []struct{ kind, body string }{
		{"note", "keep this"},
		{"decision", "drop that"},
	} {
		_, err = api.Exec(ctx, `mutation($kind: String!, $body: String!) {
			addMemory(kind: $kind, body: $body, sessionRef: "s") { id }
		}`, "", map[string]any{"kind": m.kind, "body": m.body})
		if err != nil {
			t.Fatalf("mutation: %s", err)
		}
	}

	out, err := api.Exec(ctx, `{ memories(kind: "note") { body } }`, "", nil)
	if err != nil {
		t.Fatalf("filtered query: %s", err)
	}
	if string(out) != `{"memories":[{"body":"keep this"}]}` {
		t.Fatalf("wrong filtered result: %s", out)
	}

	if _, err := api.Exec(ctx, `mutation { addMemory(kind: "bogus", body: "b", sessionRef: "s") { id } }`, "", nil); err == nil {
		t.Fatal("bad kind through GraphQL succeeded, want error")
	}
}

func TestGraphSessions(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())
	ctx := context.Background()

	out, err := api.Exec(ctx, `mutation {
		addSessionEvent(sessionId: "run1", kind: "prompt", text: "do it") { kind text }
	}`, "", nil)
	if err != nil {
		t.Fatalf("mutation: %s", err)
	}
	if string(out) != `{"addSessionEvent":{"kind":"prompt","text":"do it"}}` {
		t.Fatalf("wrong mutation result: %s", out)
	}

	out, err = api.Exec(ctx, `{ sessions { id events { kind text } } }`, "", nil)
	if err != nil {
		t.Fatalf("sessions query: %s", err)
	}
	if string(out) != `{"sessions":[{"id":"run1","events":[{"kind":"prompt","text":"do it"}]}]}` {
		t.Fatalf("wrong sessions result: %s", out)
	}

	out, err = api.Exec(ctx, `{ session(id: "run1") { id events { kind } } }`, "", nil)
	if err != nil {
		t.Fatalf("session query: %s", err)
	}
	if string(out) != `{"session":{"id":"run1","events":[{"kind":"prompt"}]}}` {
		t.Fatalf("wrong session result: %s", out)
	}

	out, err = api.Exec(ctx, `{ session(id: "missing") { id } }`, "", nil)
	if err != nil {
		t.Fatalf("missing session errored: %s", err)
	}
	if string(out) != `{"session":null}` {
		t.Fatalf("missing session must be null, got: %s", out)
	}
}

func TestGraphSessionLogRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	dir := t.TempDir()
	log := NewSessionLog(fixtureGraph(s, "test-version", dir))
	ctx := context.Background()

	if err := log.Append(ctx, "r1", "prompt", "do it"); err != nil {
		t.Fatalf("append: %s", err)
	}
	if err := log.Append(ctx, "r1", "done", "did it"); err != nil {
		t.Fatalf("append: %s", err)
	}
	// The mutation path must land in the real session file.
	raw, err := os.ReadFile(filepath.Join(dir, "r1.jsonl"))
	if err != nil {
		t.Fatalf("read file: %s", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d: %s", len(lines), raw)
	}
	if !strings.Contains(lines[0], `"kind":"prompt"`) || !strings.Contains(lines[1], `"text":"did it"`) {
		t.Fatalf("wrong file bytes: %s", raw)
	}
}

func TestGraphPlanLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())
	ctx := context.Background()
	exec := func(q string) map[string]any {
		t.Helper()
		out, err := api.Exec(ctx, q, "", nil)
		if err != nil {
			t.Fatalf("exec %s: %s", q, err)
		}
		var v map[string]any
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("decode %s: %s", out, err)
		}
		return v
	}

	made := exec(`mutation { createPlan(title: "p", goal: "g") { id state next } }`)
	p := made["createPlan"].(map[string]any)
	if p["state"] != "draft" {
		t.Fatalf("plan state=%v", p)
	}
	pid := p["id"].(string)

	made = exec(`mutation { createApproach(planId: "` + pid + `", title: "a") { id state next } }`)
	a := made["createApproach"].(map[string]any)
	if a["state"] != "draft" {
		t.Fatalf("approach state=%v", a)
	}
	aid := a["id"].(string)

	made = exec(`mutation { createTask(approachId: "` + aid + `", title: "x", prompt: "do") { id state next } }`)
	x := made["createTask"].(map[string]any)
	if x["state"] != "queued" {
		t.Fatalf("task state=%v", x)
	}
	xid := x["id"].(string)
	if next, _ := x["next"].([]any); len(next) != 2 || next[0] != "running" || next[1] != "cancelled" {
		t.Fatalf("task next=%v", x["next"])
	}

	// Illegal skip names the legal move.
	if _, err := api.Exec(ctx, `mutation { transitionTask(id: "`+xid+`", to: "done") { id } }`, "", nil); err == nil {
		t.Fatal("task skip accepted, want rejection")
	} else if !strings.Contains(err.Error(), "legal moves: running") {
		t.Fatalf("wrong error: %s", err)
	}

	// Nested tree resolves with next moves.
	got := exec(`{ plan(id: "` + pid + `") { state next approaches { state next tasks { state next } } } }`)
	tree := got["plan"].(map[string]any)
	aa := tree["approaches"].([]any)[0].(map[string]any)
	xx := aa["tasks"].([]any)[0].(map[string]any)
	if aa["state"] != "draft" || xx["state"] != "queued" {
		t.Fatalf("wrong tree: %v", tree)
	}

	// ready now requires a drafted block: undrafted is rejected naming the field.
	if _, err := api.Exec(ctx, `mutation { transitionApproach(id: "`+aid+`", to: "ready") { state } }`, "", nil); err == nil {
		t.Fatal("ready with blank block accepted, want rejection")
	} else if !strings.Contains(err.Error(), `field "current" is blank`) {
		t.Fatalf("wrong error: %s", err)
	}
	exec(`mutation { setTaskBlock(id: "` + xid + `", current: "now", target: "after", sketch: "mech", acceptance: "exit code is 0", tests: "TestX", verify: "run it", checkCommand:"true", acceptanceCommand:"test 1 -eq 1") { id } }`)

	// Owning execution facts must exist independently of lifecycle changes.
	recordGraphCompleted(t, s, xid)
	// Walk the legal chain to completion.
	for _, q := range []string{
		`mutation { transitionTask(id: "` + xid + `", to: "running") { state } }`,
		`mutation { transitionTask(id: "` + xid + `", to: "done") { state } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "ready") { state } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "running") { state } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "complete") { state } }`,
		`mutation { transitionPlan(id: "` + pid + `", to: "active") { state } }`,
		`mutation { transitionPlan(id: "` + pid + `", to: "complete") { state } }`,
	} {
		exec(q)
	}
	got = exec(`{ plan(id: "` + pid + `") { state next } }`)
	tree = got["plan"].(map[string]any)
	if tree["state"] != "complete" {
		t.Fatalf("plan state=%v", tree)
	}
	if next, _ := tree["next"].([]any); len(next) != 1 || next[0] != "retired" {
		t.Fatalf("plan next=%v", tree["next"])
	}

	// Missing ids are null, not errors.
	got = exec(`{ plan(id: "missing") { id } }`)
	if got["plan"] != nil {
		t.Fatalf("missing plan must be null, got %v", got)
	}
}

func TestGraphBadQuery(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())

	if _, err := api.Exec(context.Background(), `{ nosuchfield }`, "", nil); err == nil {
		t.Fatal("bad query succeeded, want error")
	}
	if _, err := api.Exec(context.Background(), `mutation { addMemory(kind: "x") { id } }`, "", nil); err == nil {
		t.Fatal("mutation missing required args succeeded, want error")
	}
}

func TestGraphRetire(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()
	api := fixtureGraph(s, "test-version", t.TempDir())
	ctx := context.Background()
	exec := func(q string) map[string]any {
		t.Helper()
		out, err := api.Exec(ctx, q, "", nil)
		if err != nil {
			t.Fatalf("exec %s: %s", q, err)
		}
		var v map[string]any
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("decode %s: %s", out, err)
		}
		return v
	}
	field := func(v map[string]any, mutation string) map[string]any {
		t.Helper()
		m, ok := v[mutation].(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", mutation, v)
		}
		return m
	}

	made := exec(`mutation { createPlan(title: "p", goal: "g") { id } }`)
	pid := field(made, "createPlan")["id"].(string)
	made = exec(`mutation { createApproach(planId: "` + pid + `", title: "a") { id } }`)
	aid := field(made, "createApproach")["id"].(string)
	made = exec(`mutation { createTask(approachId: "` + aid + `", title: "x", prompt: "do") { id } }`)
	xid := field(made, "createTask")["id"].(string)
	exec(`mutation { setTaskBlock(id: "` + xid + `", current: "n", target: "t", sketch: "s", acceptance: "exit code is 0", tests: "T", verify: "v", checkCommand:"true", acceptanceCommand:"test 1 -eq 1") { id } }`)
	recordGraphCompleted(t, s, xid)
	for _, q := range []string{
		`mutation { transitionTask(id: "` + xid + `", to: "running") { id } }`,
		`mutation { transitionTask(id: "` + xid + `", to: "done") { id } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "ready") { id } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "running") { id } }`,
		`mutation { transitionApproach(id: "` + aid + `", to: "complete") { id } }`,
		`mutation { transitionPlan(id: "` + pid + `", to: "active") { id } }`,
		`mutation { transitionPlan(id: "` + pid + `", to: "complete") { id } }`,
	} {
		exec(q)
	}
	made = exec(`mutation { createPlan(title: "fresh", goal: "g") { id } }`)
	fresh := field(made, "createPlan")["id"].(string)

	// Retire of incomplete rejected.
	if _, err := api.Exec(ctx, `mutation { retirePlan(id: "`+fresh+`") { id } }`, "", nil); err == nil {
		t.Fatal("retire draft accepted, want rejection")
	} else if !strings.Contains(err.Error(), "legal moves: active") {
		t.Fatalf("wrong error: %s", err)
	}

	// Retire mutations flip to terminal retired.
	got := exec(`mutation { retireApproach(id: "` + aid + `") { state next } }`)
	ra := field(got, "retireApproach")
	if ra["state"] != "retired" {
		t.Fatalf("approach state=%v", ra)
	}
	if next, _ := ra["next"].([]any); len(next) != 0 {
		t.Fatalf("retired approach next=%v", next)
	}
	got = exec(`mutation { retirePlan(id: "` + pid + `") { state next } }`)
	rp := field(got, "retirePlan")
	if rp["state"] != "retired" {
		t.Fatalf("plan state=%v", rp)
	}
	if next, _ := rp["next"].([]any); len(next) != 0 {
		t.Fatalf("retired plan next=%v", next)
	}

	// Default plans hides retired; explicit state shows it.
	planIDs := func(v map[string]any) []string {
		var out []string
		for _, p := range v["plans"].([]any) {
			out = append(out, p.(map[string]any)["id"].(string))
		}
		return out
	}
	got = exec(`{ plans { id } }`)
	if ids := planIDs(got); len(ids) != 1 || ids[0] != fresh {
		t.Fatalf("default plans=%v, want only fresh", ids)
	}
	got = exec(`{ plans(state: "retired") { id } }`)
	if ids := planIDs(got); len(ids) != 1 || ids[0] != pid {
		t.Fatalf("retired plans=%v, want only old", ids)
	}

	// Retired stays queryable by id.
	got = exec(`{ plan(id: "` + pid + `") { state } }`)
	if got["plan"].(map[string]any)["state"] != "retired" {
		t.Fatalf("retired plan by id: %v", got)
	}
	got = exec(`{ approach(id: "` + aid + `") { state } }`)
	if got["approach"].(map[string]any)["state"] != "retired" {
		t.Fatalf("retired approach by id: %v", got)
	}
}

func recordGraphCompleted(t *testing.T, s *store.Store, id string) {
	t.Helper()
	task, err := plan.GetTask(s, id)
	if err != nil {
		t.Fatal(err)
	}
	task.Execution = outcome.Completed
	task.Verification = outcome.Passed
	// Synthetic paired receipts test lifecycle plumbing; no command is run.
	raw, err := json.Marshal([]map[string]any{
		{"Kind": "check", "Command": task.CheckCommand, "Cwd": "/component", "Attempt": task.Attempts, "Started": "start", "Ended": "end", "episode_started": "start", "episode_ended": "end", "ExitCode": 0, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed", "acceptance": map[string]any{"command": task.AcceptanceCommand, "cwd": "/component", "started": "start", "ended": "end", "exit_code": 0, "status": "passed"}},
		{"Kind": "review", "Attempt": task.Attempts, "Outcome": map[string]any{"Status": "completed"}, "Verification": "passed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	task.AttemptLog = string(raw)
	if err := plan.SetTaskOutcome(s, task); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareTaskAdmissionAndRetainedEffects(t *testing.T) {
	for _, scenario := range []string{"blank", "blank_acceptance", "typed", "rows", "bytes", "aliases", "source", "later", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			api, err := New(s, "test", t.TempDir()).Scoped(t.TempDir(), "preparation", true)
			if err != nil {
				t.Fatal(err)
			}
			g := config.Defaults().Tools.GraphQL
			input, document := freshTaskValues(), prepareDocument
			published := false
			switch scenario {
			case "blank":
				input["taskPrompt"] = "\t\n"
			case "blank_acceptance":
				input["acceptance"] = " \n"
			case "typed":
				input["taskTitle"] = 7
			case "rows":
				g.MaxRows = 3
			case "bytes":
				g.MaxResultBytes = 500
			case "aliases":
				g.MaxRows = 4
				document = `mutation($input:FreshTaskInput!) { fresh:prepareTask(input:$input) { first:task{id} second:task{id} } }`
				published = true
			case "source":
				input["current"] = strings.Repeat("界", 900)
				g.MaxResultBytes = 1600
				published = true
			case "later":
				document = strings.TrimSuffix(prepareDocument, " }") + ` later:transitionPlan(id:"absent",to:"active"){id} }`
				published = true
			case "closed":
				s.Close()
			}
			out, err := api.ExecBounded(context.Background(), document, "", map[string]any{"input": input}, g)
			if err == nil || len(out) > g.MaxResultBytes {
				t.Fatalf("expected bounded refusal: %s %v", out, err)
			}
			var receipt Result
			if e := json.Unmarshal(out, &receipt); e != nil {
				t.Fatal(e)
			}
			if scenario == "closed" {
				if len(receipt.Effects) != 4 || receipt.Effects[0].Status != "unknown" {
					t.Fatalf("entered owner concealed: %s", out)
				}
				for _, effect := range receipt.Effects[1:] {
					if effect.Status != "unexecuted" || effect.ID != "" {
						t.Fatalf("tail invented: %s", out)
					}
				}
				return
			}
			plans, e := plan.ListPlans(s, "")
			if e != nil {
				t.Fatal(e)
			}
			if !published {
				if len(plans) != 0 {
					t.Fatalf("prevalidation published: %+v %s", plans, out)
				}
				for _, effect := range receipt.Effects {
					if effect.Status != "unexecuted" || effect.ID != "" {
						t.Fatalf("false effect: %s", out)
					}
				}
				return
			}
			if len(plans) != 1 || len(receipt.Effects) < 4 {
				t.Fatalf("actual effects missing: %s %+v", out, plans)
			}
			for i, step := range []string{"createPlan", "createApproach", "createTask", "setTaskBlock"} {
				effect := receipt.Effects[i]
				if effect.Key != "fresh" || effect.Field != "prepareTask" || effect.Step != step || effect.Status != "applied" || effect.ID == "" {
					t.Fatalf("lost step: %s", out)
				}
			}
			if receipt.Effects[0].ID != plans[0].ID || receipt.Effects[2].ID != receipt.Effects[3].ID {
				t.Fatalf("invented IDs: %s", out)
			}
			actual, e := plan.GetTask(s, receipt.Effects[3].ID)
			if e != nil || actual.Current != input["current"] || actual.Qualified() {
				t.Fatalf("raw owner differs: %+v %v", actual, e)
			}
			if scenario == "later" && (len(receipt.Effects) != 5 || receipt.Effects[4].Status != "rolled_back") {
				t.Fatalf("later error erased receipts: %s", out)
			}
			t.Logf("actual bounded partial result: %s", out)
		})
	}
}

func TestPrepareTaskQueuedCancellationAndReset(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, "test", t.TempDir()).Scoped(t.TempDir(), "preparation", true)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- s.Update(func(*store.Tx) error { close(entered); <-release; return nil }) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	g := config.Defaults().Tools.GraphQL
	out, err := api.ExecBounded(ctx, prepareDocument, "", map[string]any{"input": freshTaskValues()}, g)
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	var receipt Result
	if e := json.Unmarshal(out, &receipt); e != nil || !errors.Is(err, context.DeadlineExceeded) || len(receipt.Effects) != 4 {
		t.Fatalf("deadline/receipts lost: %s %v %v", out, err, e)
	}
	if receipt.Effects[0].Status != "unknown" {
		t.Fatalf("entered owner not disclosed: %s", out)
	}
	for _, effect := range receipt.Effects[1:] {
		if effect.Status != "unexecuted" || effect.ID != "" {
			t.Fatalf("cancelled tail invented: %s", out)
		}
	}
	plans, e := plan.ListPlans(s, "")
	if e != nil || len(plans) != 0 {
		t.Fatalf("cancelled queue committed: %+v %v", plans, e)
	}
	out, err = api.ExecBounded(context.Background(), prepareDocument, "", map[string]any{"input": freshTaskValues()}, g)
	if err != nil {
		t.Fatalf("fresh request inherited deadline: %s %v", out, err)
	}
	plans, e = plan.ListPlans(s, "")
	if e != nil || len(plans) != 1 {
		t.Fatalf("fresh owner failed: %+v %v", plans, e)
	}
}
