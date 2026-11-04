package prompts

import (
	"strings"
	"testing"
)

func TestPlanningAndPATContractsNameCurrentActions(t *testing.T) {
	pc := Builtin()
	prepare := pc.Text("system.task.prepare")
	for _, need := range []string{"Source-changing and command tools are not advertised during drafting", "After claim, the host advertises"} {
		if !strings.Contains(prepare, need) {
			t.Errorf("planning role lacks honest tool surface %q: %s", need, prepare)
		}
	}
	another := pc.Text("planning.another")
	for _, need := range []string{"revisionContract", "requestRevision", "current snapshot"} {
		if !strings.Contains(another, need) {
			t.Errorf("draft-task question does not name the revision path %q: %s", need, another)
		}
	}
	bookkeeping := pc.Text("decision.bookkeeping.instructions")
	for _, need := range []string{"Checkpoint.NextAction already names", "PreflightRefusal", "call repair, not by itself missing PAT"} {
		if !strings.Contains(bookkeeping, need) {
			t.Errorf("PAT judge lacks current-action distinction %q: %s", need, bookkeeping)
		}
	}
	discovery := pc.Text("decision.discovery.instructions")
	for _, need := range []string{"Failed=false", "StateChanged=true", "short acknowledgement"} {
		if !strings.Contains(discovery, need) {
			t.Errorf("discovery judge lacks successful-acknowledgement rule %q: %s", need, discovery)
		}
	}
	if got := pc.Text("planning.interface"); !strings.Contains(got, "synonyms are not aliases") {
		t.Errorf("planning interface lacks exact-argument rule: %s", got)
	}
	if got := pc.Text("system.coding.knowledge"); !strings.Contains(got, "Do not invent fields") {
		t.Errorf("knowledge guidance lacks exact GraphQL rule: %s", got)
	}
	if got := pc.Text("help.bash"); !strings.Contains(got, "expected-failing negative controls in separate calls") {
		t.Errorf("bash help lacks separate negative-control rule: %s", got)
	}
}
