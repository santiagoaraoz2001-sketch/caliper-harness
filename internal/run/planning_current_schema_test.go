package run

import (
	"specific-harness/internal/tool"
	"testing"
)

func TestPlanningNativeSchemaIsExactCurrentQuestion(t *testing.T) {
	f := newPlanningFixture(t)
	f.task()
	f.call(tool.Args{"current": "broken", "target": "working"})
	f.call(tool.Args{"acceptance": "behavior observed", "tests": "planned", "acceptance_command": "true"})
	d := f.definition()
	r := tool.NewRegistry(f.d.Prompts)
	r.Register(d)
	native, err := r.NativeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	properties := native[0].Function.Parameters["properties"].(map[string]any)
	if len(properties) != 1 || properties["objective"] == nil {
		t.Fatalf("native schema is not current one-field question: %+v", properties)
	}
	if args, repair := tool.ValidateJSON(d, `{"objective":"fix behavior","execution":"do not discard"}`); repair == "" || args != nil {
		t.Fatalf("accepted non-current answer: %v %s", args, repair)
	}
	if f.definition().Schema() != d.Schema() {
		t.Fatal("refusal advanced the question")
	}
	f.call(tool.Args{"objective": "fix behavior"})
	if got := f.definition().Params; len(got) != 1 || got[0].Name != "execution" {
		t.Fatal(got)
	}
}
