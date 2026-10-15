package tools

import (
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestHelpCurrentPlanCallPrecedesGuidanceAndRejectsFutureFieldsPrecisely(t *testing.T) {
	pc := prompts.Builtin()
	reg := tool.NewRegistry(pc)
	entry := tool.Definition{Name: "plan", Prompts: pc, Desc: pc.Text("tools.plan.description"), Purpose: pc.Text("planning.entry")}
	reg.Register(entry)
	h := helpDefinition(config.Defaults(), reg, pc)
	full, err := h.Execute(t.Context(), tool.Args{"topic": "plan"})
	if err != nil {
		t.Fatal(err)
	}
	call := nativeHelpTemplate(t, full.Text)
	if call.Name != "plan" || len(call.Arguments) != 0 || !strings.Contains(full.Text, "This step takes no arguments") {
		t.Fatalf("current entry help lacks its exact empty call: %s", full.Text)
	}
	if schema, guide := strings.Index(full.Text, "Current schema:"), strings.Index(full.Text, "## help.plan"); schema < 0 || schema >= guide {
		t.Fatalf("future guidance preceded the current definition: %s", full.Text)
	}
	for _, future := range []string{"more=true", "more=false", "invoke=", "plan(execution=", "EXECUTE"} {
		if strings.Contains(full.Text, future) {
			t.Errorf("entry help advertises future %q: %s", future, full.Text)
		}
	}
	compact, err := h.Execute(t.Context(), tool.Args{"topic": "schema.plan"})
	if err != nil || nativeHelpTemplate(t, compact.Text).Name != call.Name || len(nativeHelpTemplate(t, compact.Text).Arguments) != 0 {
		t.Fatalf("compact and full help disagree: %s %v", compact.Text, err)
	}
	_, repair := tool.ValidateJSON(entry, `{"more":true}`)
	if !strings.Contains(repair, "This step takes no arguments") || strings.Contains(repair, "accepted arguments: .") || strings.Contains(repair, "slots:") {
		t.Fatalf("empty-field refusal is cryptic: %s", repair)
	}
	rejected := nativeHelpTemplate(t, repair)
	if rejected.Name != "plan" || len(rejected.Arguments) != 0 {
		t.Fatal("refusal did not reuse the same current empty call", repair)
	}
	_, textRepair := tool.Validate(entry, map[string]string{"more": "true"})
	if !strings.Contains(textRepair, `plan: remove unsupported argument "more"`) || !strings.Contains(textRepair, "This step takes no arguments") || len(nativeHelpTemplate(t, textRepair).Arguments) != 0 {
		t.Fatal("text refusal changed the actual tool/argument or lost the current empty call", textRepair)
	}
	// The next live question replaces the entry definition without a new help
	// source or any copied future fields.
	next := tool.NewRegistry(pc)
	next.Register(tool.Definition{Name: "plan", Prompts: pc, Desc: "answer the current objective", Purpose: "What outcome is required now?", Params: []tool.Param{{Name: "objective", Type: tool.TypeString, Required: true, Desc: "current outcome"}}})
	h = helpDefinition(config.Defaults(), reg, pc, func() *tool.Registry { return next })
	full, err = h.Execute(t.Context(), tool.Args{"topic": "plan"})
	if err != nil || nativeHelpTemplate(t, full.Text).Arguments["objective"] == nil || !strings.Contains(full.Text, "What outcome is required now?") || strings.Contains(full.Text, "returns the first question") {
		t.Fatalf("help kept a prior question/template: %s %v", full.Text, err)
	}
	ref, err := h.Execute(t.Context(), tool.Args{"topic": "reference.pat"})
	if err != nil || !strings.Contains(ref.Text, `plan(invoke="EXECUTE")`) || !strings.Contains(ref.Text, "more=true") {
		t.Fatal("full PAT use cases are no longer reachable", ref.Text, err)
	}
}

func TestHelpCurrentNavigationExamplesAndCompatibilityFinder(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	active := reg.View([]string{"read", "find_file", "help"})
	h := helpDefinition(cfg, reg, prompts.Builtin(), func() *tool.Registry { return active })
	for _, topic := range []string{"schema.read.definition", "schema.read.outline"} {
		res, err := h.Execute(t.Context(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatal(err)
		}
		call := nativeHelpTemplate(t, res.Text)
		if call.Name != "read" || call.Arguments["path"] == nil {
			t.Fatal("topic projection lost the actual required read argument", res.Text)
		}
		if topic == "schema.read.definition" && call.Arguments["definition"] != "<definition>" || topic == "schema.read.outline" && call.Arguments["outline"] != true {
			t.Fatal("topic template omitted its requested read action", res.Text)
		}
	}
	for _, topic := range []string{"find", "schema.find"} {
		res, err := h.Execute(t.Context(), tool.Args{"topic": topic})
		if err != nil || strings.Contains(res.Text, "unavailable in the current tool view") {
			t.Fatalf("registered finder compatibility is falsely unavailable: %s %v", res.Text, err)
		}
		if strings.Contains(res.Text, "only through an explicit compatible full registry") || strings.Contains(res.Text, "full-view-only") {
			t.Fatal("available compatibility finder prose incorrectly requires the full view", res.Text)
		}
		if got := nativeHelpTemplate(t, res.Text); got.Name != "find_file" || got.Arguments["pattern"] == nil {
			t.Fatal("compatibility help does not use the actual canonical current schema", res.Text)
		}
	}
}
