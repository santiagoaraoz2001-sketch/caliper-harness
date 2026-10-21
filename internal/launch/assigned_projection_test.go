package launch

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

// Real claim/store/settlement owners; check and reviewer receipts are explicit
// component doubles. This validates the advisory projection, not model quality.
func TestAssignedFactsBriefProjection(t *testing.T) {
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) {
		return "PASS: synthetic projection fixture", nil
	}))
	d.Wf.VerifyFollowup = true
	a := &Assigned{Deps: d, Root: root}
	identity, _, err := a.Start(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		facts, err := a.Facts(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(facts)
		t.Logf("assignment-owner-projection: %s", raw)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}
	assertFull := func(fields map[string]json.RawMessage) {
		t.Helper()
		var frozen map[string]string
		json.Unmarshal(fields["Frozen"], &frozen)
		if frozen["prompt"] != a.task.Prompt || frozen["acceptance_command"] != a.task.AcceptanceCommand {
			t.Fatal("active/unqualified requirements lost", string(fields["Frozen"]))
		}
	}
	before, _ := json.Marshal(a.task)
	live := read()
	assertFull(live)
	if _, present := live["Current"]; present || string(live["CurrentMatchesFrozen"]) != "true" {
		t.Error("identical live brief repeated instead of explicit equality")
	}
	zero := 0
	c := &session.CodingState{TaskID: identity.TaskID, Attempt: identity.Attempt, Workspace: root,
		Command: a.task.CheckCommand, AcceptanceCommand: a.task.AcceptanceCommand, Revision: 1}
	c.Receipt = &session.CheckReceipt{Command: c.Command, Cwd: root, Revision: 1, Status: "passed", Started: "fixture-start", Ended: "fixture-end", EpisodeStarted: "fixture-start", EpisodeEnded: "fixture-end", ExitCode: &zero,
		Acceptance: &session.CommandReceipt{Command: c.AcceptanceCommand, Cwd: root, Started: "fixture-start", Ended: "fixture-end", Status: "passed", ExitCode: &zero}}
	settled, err := a.Settle(t.Context(), outcome.Outcome{Status: outcome.Completed}, "component execution", "fixture request", "fixture evidence", nil, c)
	if err != nil || !settled.Qualified {
		t.Fatalf("fixture did not qualify: %+v %v", settled, err)
	}
	history := read()
	if _, present := history["Frozen"]; present || len(history["BriefBasis"]) == 0 || !strings.Contains(string(history["BriefBasis"]), "sha256=") || len(history["Identity"]) == 0 || string(history["Qualified"]) != "true" {
		t.Error("settled qualified assignment repeats full prose or lacks provenance", history)
	}
	if string(history["Title"]) != `"t0"` || string(history["Target"]) != `"after"` {
		t.Error("settled task title/target missing")
	}
	// A past attempt or changed frozen brief must never be mistaken for the
	// newly qualified attempt, even when the owning Task currently qualifies.
	identity.Attempt--
	assertFull(read())
	identity.Attempt++
	original := a.task.Prompt
	a.task.Prompt = "prior, different frozen requirement"
	changed := read()
	assertFull(changed)
	if len(changed["Current"]) == 0 {
		t.Error("changed current brief hidden behind equality")
	}
	a.task.Prompt = original
	after, _ := json.Marshal(a.task)
	if string(before) != string(after) {
		t.Fatal("projection changed frozen owner history")
	}
	if _, err := plan.TransitionTask(d.Store, identity.TaskID, plan.TaskFailed); err != nil {
		t.Fatal(err)
	}
	failed := read()
	assertFull(failed)
	if string(failed["Qualified"]) != "false" || string(failed["TaskState"]) != `"failed"` {
		t.Fatal("reworked state was portrayed as qualified history")
	}
}
