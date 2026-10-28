package prompts

import (
	"os"
	"strings"
	"testing"
)

func TestInactiveReadyGuidePreservesOlderCustomCatalog(t *testing.T) {
	path := catalogFile(t, func(m map[string]string) {
		delete(m, "planning.ready_inactive")
		m["planning.ready"] = "My existing active-parent guidance"
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if pc.Has("planning.ready_inactive") || pc.Text("planning.ready") != "My existing active-parent guidance" {
		t.Fatal("custom wording replaced")
	}
	if err := CompatibleWith(path, catalogFile(t, nil)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("catalog rewritten")
	}
	if _, err := Load(catalogFile(t, func(m map[string]string) { m["planning.ready_inactive"] = "" })); err == nil {
		t.Fatal("explicit empty guide accepted")
	}
}

func TestReadinessInstructionsDoNotRequireExcludedExecution(t *testing.T) {
	pc := Builtin()
	for _, key := range []string{"system.task.prepare", "planning.ready", "planning.ready_inactive", "help.ready_task", "tools.ready_task.purpose", "preparation.ready_task", "decision.bookkeeping.instructions"} {
		text := strings.ToLower(pc.texts[key])
		if !strings.Contains(text, "readiness-only") {
			t.Errorf("%s omits readiness endpoint", key)
		}
	}
	role := pc.Text("system.task.prepare")
	if strings.Index(role, "requested endpoint") > strings.Index(role, "latest host-published") {
		t.Fatal("next question overrides requested endpoint")
	}
	for _, key := range []string{"tools.task_start.purpose", "help.task_start", "preparation.claim_task"} {
		if !strings.Contains(strings.ToLower(pc.texts[key]), "requested execution") {
			t.Errorf("%s grants an unconditional claim", key)
		}
	}
	if strings.Contains(pc.texts["tool.call.template"], "Call ") {
		t.Fatal("argument example mandates another call")
	}
	if !strings.Contains(pc.Text("decision.bookkeeping.instructions"), "Unclaimed tasks beyond that endpoint are not missing updates") {
		t.Fatal("judge treats requested stop as incomplete bookkeeping")
	}
}
