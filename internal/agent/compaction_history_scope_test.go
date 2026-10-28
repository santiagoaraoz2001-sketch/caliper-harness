package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// Exercise the real summary request and replacement boundary. The model fixture
// is not a claim that a natural model obeys the new historical-scope contract.
func TestCompactionPartialHistoryKeepsLaterRecoveryAuthority(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "Implement the requested behavior, then report the actual check.", false)
	c.Add("reply", "assistant", strings.Repeat("Old implementation discussion. ", 1000), false)
	c.Add("failure", "user", "Earlier browser jump failed; system unchanged.", false)
	c.Add("output", "user", "Later browser jump passed; system changed and fuel 100 to 92.", false)
	c.Add("reply", "assistant", "Delivered README and playable build; visual inspection remains unverified.", false)
	c.Add("evidence", "user", "Current declared check revision 7 passed; coverage unknown.", false)
	c.Coding = &session.CodingState{TaskID: "task", Revision: 7, Closed: true}
	before := c.Clone()
	m := &scriptModel{replies: []string{"Earlier browser jump failed during implementation; later outcomes are outside this historical slice."}}
	r := truthRunner(t, m, 50000)
	r.Prompts = prompts.Builtin()
	r.Compaction.Keep = 1
	boundary := keepStartBudget(c.Messages(), c.Estimate(), 500)
	if boundary <= 1 || boundary > 3 {
		t.Fatalf("fixture lost old/tail separation: %d", boundary)
	}
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran || len(m.seen) != 1 {
		t.Fatalf("ran=%v calls=%d err=%v", ran, len(m.seen), err)
	}
	instruction, material := m.seen[0][0].Content, m.seen[0][1].Content
	wantScope := fmt.Sprintf("precedes retained entry %d of %d", boundary, len(c.Entries))
	for _, want := range []string{wantScope, "later history not supplied here", "cannot establish global latest, unresolved or not-done status", "absent resolution here does not mean still unresolved"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("actual summary request lacks scope %q", want)
		}
	}
	if strings.Contains(material, c.Entries[3].Message.Content) || strings.Contains(material, c.Entries[4].Message.Content) {
		t.Fatal("repair must not duplicate the retained success/handoff tail into summary input")
	}
	for _, e := range c.Entries[2:] {
		found := false
		for _, got := range next.Entries {
			found = found || reflect.DeepEqual(got, e)
		}
		if !found {
			t.Errorf("historical failure/current recovery authority lost: %+v", e)
		}
	}
	if !reflect.DeepEqual(next.Coding, c.Coding) || !reflect.DeepEqual(c, before) {
		t.Fatal("compaction changed owning state/input")
	}
	for _, e := range next.Entries {
		if e.Kind == "summary" && !strings.Contains(e.Message.Content, "Historical partial view") {
			t.Fatal("summary lost scoped provenance")
		}
	}
	judge := r.Prompts.Text("decision.compaction.instructions")
	if !strings.Contains(judge, "later retained successes may be absent") || !strings.Contains(judge, "Reject global latest") {
		t.Fatal("judge contract contradicts summary scope")
	}
}
