package run

import (
	"bytes"
	"encoding/json"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestProgressInventoryPreservesUnknownFieldsAndUnqualifiedProse(t *testing.T) {
	raw := json.RawMessage(`{"active":[{"goal":"preserve every article","extension":9007199254740993,"approaches":[{"title":"actual coverage/strategy","extension":{"x":"unchanged"},"tasks":[{"id":"qualified","state":"done","qualified":true,"title":"keep title","target":"keep target","prompt":"settled execution prose","futureField":{"number":9007199254740993}},{"id":"unverified","state":"done","qualified":false,"prompt":"must retain failed proof"},{"id":"running","state":"running","qualified":true,"prompt":"must retain active requirements"},{"id":"unknown","state":"done","prompt":"missing qualification is not success"}]}]}]}`)
	before := append([]byte(nil), raw...)
	projected, err := progressInventory(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{[]byte(`9007199254740993`), []byte(`"futureField"`), []byte(`"extension"`), []byte(`"actual coverage/strategy"`), []byte(`"must retain failed proof"`), []byte(`"must retain active requirements"`), []byte(`"missing qualification is not success"`)} {
		if !bytes.Contains(projected, required) {
			t.Fatalf("unknown field or unqualified evidence lost: %s", projected)
		}
	}
	if bytes.Contains(projected, []byte(`"settled execution prose"`)) || !bytes.Contains(projected, []byte(`"settledBriefOmission"`)) || !bytes.Equal(before, raw) {
		t.Fatal("projection omitted the wrong field, lost provenance or mutated its source")
	}
}

func TestProgressInventoryKeepsCurrentBriefAndProjectsQualifiedHistory(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	id := draftPipelineTask(t, f)
	f.call(tool.Args{"invoke": "COMPLETE"})
	a, _ := plan.GetApproach(f.d.Store, f.scope.ID)
	p, _ := plan.ApproachPlan(f.d.Store, a.ID)
	r := consistencyRunner(t, f, &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}, "coding")
	read := func(stage string, concise bool) {
		t.Helper()
		before, _ := plan.GetTask(f.d.Store, id)
		facts, err := r.Assigned.Facts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		view := facts.(agent.BookkeepingFacts)
		raw, _ := json.Marshal(map[string]any{"stage": stage, "facts": view, "checkpoint": view.Checkpoint, "next_action": view.Preparation})
		t.Logf("current-owner-projection: %s", raw)
		var groups map[string][]struct {
			Goal       string
			Approaches []struct {
				Title string
				Tasks []map[string]any
			}
		}
		if err := json.Unmarshal(view.Unfinished, &groups); err != nil {
			t.Fatal(err)
		}
		got := groups["active"][0]
		task := got.Approaches[0].Tasks[0]
		if got.Goal != p.Goal || got.Approaches[0].Title != a.Title || task["id"] != id || task["target"] != before.Target || task["title"] != before.Title {
			t.Fatal("goal, coverage, strategy or task identity lost")
		}
		_, hasPrompt := task["prompt"]
		if concise && (hasPrompt || task["settledBriefOmission"] == nil || task["qualified"] != true) {
			t.Error("qualified historical execution prose repeated or provenance missing")
		}
		if !concise && (!hasPrompt || task["prompt"] != before.Prompt || task["verify"] != before.Verify || task["sketch"] != before.Sketch) {
			t.Error("current/unqualified task requirements lost")
		}
		after, _ := plan.GetTask(f.d.Store, id)
		b, _ := json.Marshal(before)
		c, _ := json.Marshal(after)
		if string(b) != string(c) {
			t.Fatal("projection wrote to owning graph")
		}
	}
	read("ready", false)
	qualifyPipelineTask(t, f, id)
	read("qualified", true)
	if _, err := plan.TransitionTask(f.d.Store, id, plan.TaskFailed); err != nil {
		t.Fatal(err)
	}
	read("explicit-rework", false)
}
