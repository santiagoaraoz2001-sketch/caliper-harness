package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestCheckSchemaAdvertisesFrozenReportWithoutNewArguments(t *testing.T) {
	frozen := checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"browser-original"}}
	r := &Runner{Prompts: prompts.Builtin(), CheckReport: frozen.Clone(), Continuation: session.Continuation{Coding: &session.CodingState{Attempt: 1, Command: "fixture", Report: checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"historical-contract"}}}}}
	bashDef := bash.NewAt(config.Defaults().Tools.Bash, t.TempDir())
	d := r.CheckDefinition(bashDef)
	registry := tool.NewRegistry()
	registry.Register(d)
	definitions, err := registry.NativeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(definitions)
	for _, want := range []string{"browser-original", checkreport.StageFormat, "CALIPER_TEST_RUN_ID"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("actual native schema omits %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "historical-contract") {
		t.Fatal("historical CodingState replaced the captured current owner contract")
	}
	for _, p := range d.Params {
		if p.Name != "cmd" && p.Name != "acceptance_cmd" && p.Name != "timeout" {
			t.Fatalf("new model-owned contract argument: %s", p.Name)
		}
	}
	r.CheckReport = checkreport.Contract{}
	r.Continuation.Coding = nil
	if strings.Contains(r.CheckDefinition(bashDef).Purpose, "Configured-suite execution contract") {
		t.Fatal("default opaque command gained a report requirement")
	}
}
