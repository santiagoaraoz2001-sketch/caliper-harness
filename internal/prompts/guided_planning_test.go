package prompts

import (
	"strings"
	"testing"
)

func TestGuidedPlanningCatalogRolesAndQuestions(t *testing.T) {
	pc := Builtin()
	for _, phase := range []string{"entry", "goal", "approach", "task", "another", "select", "select_approach", "observe", "proof", "step_objective", "step_execution", "step_validation", "step_more", "review", "task_drafted", "task_complete", "ready", "validated", "plan_complete", "settled", "remediate", "stopped"} {
		if !pc.Has("planning."+phase) || strings.TrimSpace(pc.Text("planning."+phase)) == "" {
			t.Fatal("missing current phase", phase)
		}
	}
	for _, x := range []struct{ key, need string }{{"system.coding.direct", "no PAT"}, {"system.task.prepare", "main model"}, {"planning.proof", "behavioral command"}, {"planning.step_more", "no fixed count"}, {"planning.ready", "independent review"}, {"planning.remediate", "existing task"}, {"planning.validated", "Report changes to the user"}, {"help.check", "Ordinary coding"}} {
		if !strings.Contains(pc.Text(x.key), x.need) {
			t.Fatalf("%s lacks %q", x.key, x.need)
		}
	}
	for _, contract := range []string{"acceptance_cmd first, then the configured cmd even if acceptance fails", "Cancellation or receipt-persistence failure stops the episode", "Both commands must exit0", "fresh whole episode", "Failed/denied episodes are not automatically retried"} {
		if !strings.Contains(pc.Text("help.check"), contract) {
			t.Fatalf("check help lost paired-check contract %q", contract)
		}
	}
	path := catalogFile(t, func(m map[string]string) { m["planning.step_execution"] = "Editable execution question" })
	custom, e := Load(path)
	if e != nil || custom.Text("planning.step_execution") != "Editable execution question" {
		t.Fatal("phase question not editable", e)
	}
}
