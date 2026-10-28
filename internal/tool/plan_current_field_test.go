package tool

import (
	"specific-harness/internal/prompts"
	"strings"
	"testing"
)

func TestPlanRefusesFutureAnswerWithoutSilentLoss(t *testing.T) {
	d := Definition{Name: "plan", Prompts: prompts.Builtin(), Params: []Param{{Name: "objective", Type: TypeString, Required: true}}}
	args, repair := ValidateJSON(d, `{"objective":"fix docking","execution":"preserve this authored implementation"}`)
	if repair == "" || args != nil || !strings.Contains(repair, "execution") || !strings.Contains(repair, "objective") {
		t.Fatalf("future answer silently accepted/lost or current recovery omitted: args=%v repair=%q", args, repair)
	}
	args, repair = ValidateJSON(d, `{"objective":"fix docking"}`)
	if repair != "" || args["objective"] != "fix docking" {
		t.Fatalf("legal correction rejected: %v %s", args, repair)
	}
}
