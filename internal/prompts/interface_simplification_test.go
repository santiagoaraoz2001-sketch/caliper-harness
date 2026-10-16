package prompts

import (
	"strings"
	"testing"
)

func TestInterfaceSimplificationKeepsEntryCurrentAndPlanningBoundary(t *testing.T) {
	pc := Builtin()
	if got := strings.TrimSpace(pc.Text("planning.entry")); got != "Call plan with {}; returns the first question." {
		t.Fatalf("entry competes with later questions: %s", got)
	}
	for _, key := range []string{"planning.entry", "help.plan", "system.task.prepare", "system.workflow.focused", "preparation.guided_next"} {
		text := pc.Text(key)
		for _, future := range []string{"more=true", "more=false", "invoke=", "plan(execution=", "EXECUTE", "DRAFT TASKS"} {
			if strings.Contains(text, future) {
				t.Errorf("%s includes a future call instead of the current question: %q", key, future)
			}
		}
	}
	for _, key := range []string{"help.plan", "system.task.prepare", "system.workflow.focused", "system.coding.direct", "preparation.guided_next"} {
		text := strings.ToLower(strings.Join(strings.Fields(pc.Text(key)), " "))
		if !strings.Contains(text, "all task briefs") || !strings.Contains(text, "readiness") || !strings.Contains(text, "claim") || strings.Contains(text, "pre-claim effects") {
			t.Errorf("%s does not describe complete Approach drafting before execution: %s", key, text)
		}
	}
	ref := pc.Text("help.reference.pat")
	for _, preserved := range []string{"more=true", "more=false", `invoke="TASK"`, `invoke="EXECUTE"`, "independent reviewer PASS", "REMEDIATE", "Stops need authorized recovery"} {
		if !strings.Contains(ref, preserved) {
			t.Errorf("full PAT reference lost %q", preserved)
		}
	}
}

func TestInterfaceSimplificationDistinctProgressChoices(t *testing.T) {
	pc := Builtin()
	for key, want := range map[string]string{
		"decision.discovery.continue":   "continue productive work",
		"decision.discovery.act":        "repair the latest rejected call",
		"decision.discovery.reconsider": "reconsider the unsuccessful approach",
	} {
		if got := pc.Text(key); got != want {
			t.Errorf("%s is not the approved distinct direction: %q", key, got)
		}
	}
	if repair := pc.Text("continuation.discovery.act"); !strings.Contains(repair, "actual refusal") || !strings.Contains(repair, "current schema") || strings.Contains(repair, "EXECUTE") {
		t.Error("rejected-call advice is not grounded in its current refusal/schema", repair)
	}
	if reconsider := pc.Text("continuation.discovery.reconsider"); !strings.Contains(reconsider, "Deliberate") || !strings.Contains(reconsider, "different grounded method") || strings.Contains(reconsider, "carry out the next") {
		t.Error("reconsider advice is still another execution demand", reconsider)
	}
}
