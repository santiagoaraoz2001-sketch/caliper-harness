package tool

import (
	"specific-harness/internal/prompts"
	"testing"
)

// Captured from the 2026-10-15 native Laguna run: the plan tool's legal
// arguments change per question, so the model answers with a synonym
// (method) or carries fields from another question. Unambiguous synonyms
// map to the current field; non-current authored answers must never be dropped.
func TestPlanArgumentCompatibilitySpellings(t *testing.T) {
	pc := prompts.Builtin()
	execution := Definition{Name: "plan", Prompts: pc, Params: []Param{{Name: "execution", Type: TypeString, Required: true}}}
	args, repair := ValidateJSON(execution, `{"method":"run the regression first"}`)
	if repair != "" || args["execution"] != "run the regression first" {
		t.Fatalf("method not mapped to execution: args=%v repair=%q", args, repair)
	}
	proof := Definition{Name: "plan", Prompts: pc, Params: []Param{{Name: "acceptance", Type: TypeString, Required: true}, {Name: "tests", Type: TypeString, Required: true}, {Name: "acceptance_command", Type: TypeString, Required: true}}}
	args, repair = ValidateJSON(proof, `{"acceptance":"a","tests":"t","acceptance_command":"npm test","objective":"stale earlier answer","title":"stale"}`)
	if repair == "" || args != nil {
		t.Fatalf("non-current answers silently discarded: args=%v repair=%q", args, repair)
	}
	if _, repair = ValidateJSON(proof, `{"objective":"only a stale answer"}`); repair == "" {
		t.Fatal("stale fields alone must not satisfy the current question")
	}
	entry := Definition{Name: "plan", Prompts: pc}
	if _, repair = ValidateJSON(entry, `{"prompt":"start planning"}`); repair == "" {
		t.Fatal("entry silently discarded authored prompt")
	}
	if _, repair = ValidateJSON(execution, `{"execution":"x","bogus":"y"}`); repair == "" {
		t.Fatal("genuinely unknown argument was accepted")
	}
	other := Definition{Name: "read", Prompts: pc, Params: []Param{{Name: "path", Type: TypeString, Required: true}}}
	if _, repair = ValidateJSON(other, `{"path":"x","objective":"stale"}`); repair == "" {
		t.Fatal("planning tolerance leaked into other tools")
	}
}
