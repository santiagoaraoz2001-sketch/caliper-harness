package tool

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestOpeningNormalizationPreservesArgumentsAndDisclosesDiscardedNames(t *testing.T) {
	d := Definition{Name: "help", Opening: true, Params: []Param{{Name: "topic", Type: TypeString}}}
	in := Args{"TOPIC": "schema.read", "query": "x", "extra": true}
	got, ignored := NormalizeOpening(d, in)
	if !reflect.DeepEqual(got, Args{"TOPIC": "schema.read"}) || !reflect.DeepEqual(ignored, []string{"extra", "query"}) || len(in) != 3 {
		t.Fatalf("got %v ignored %v original %v", got, ignored, in)
	}
	raw, _ := json.Marshal(got)
	args, gap := ValidateJSON(d, string(raw))
	if gap != "" || args["topic"] != "schema.read" {
		t.Fatalf("args %v gap %s", args, gap)
	}
	for _, input := range []Args{{"topic": true}, {"topic": "read", "TOPIC": "write"}} {
		got, _ := NormalizeOpening(d, input)
		raw, _ := json.Marshal(got)
		if _, gap := ValidateJSON(d, string(raw)); gap == "" {
			t.Fatalf("invalid supported args accepted: %v", input)
		}
	}
}

func TestOpeningNormalizationDoesNotInferNoArgumentToolsAreHarmless(t *testing.T) {
	for _, name := range []string{"check", "task_start", "plan"} {
		d := Definition{Name: name}
		input := Args{"query": "x"}
		got, ignored := NormalizeOpening(d, input)
		if !reflect.DeepEqual(got, input) || len(ignored) != 0 {
			t.Fatalf("non-opening %s changed: %v %v", name, got, ignored)
		}
		if _, gap := ValidateJSON(d, `{"query":"x"}`); gap == "" {
			t.Fatalf("non-opening %s silently accepted unsupported arguments", name)
		}
	}
}
