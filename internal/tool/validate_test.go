package tool

import (
	"strings"
	"testing"
)

func TestRepairsRestateBlockShape(t *testing.T) {
	d := Definition{Name: "echo", Params: []Param{
		{Name: "text", Desc: "text", Type: TypeString, Required: true},
		{Name: "n", Desc: "count", Type: TypeInt},
	}}
	cases := []struct {
		name string
		raw  map[string]string
	}{
		{"unknown slot", map[string]string{"bogus": "x"}},
		{"missing required", map[string]string{}},
		{"bad int", map[string]string{"text": "hi", "n": "abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, q := Validate(d, tc.raw)
			if q == "" {
				t.Fatal("expected a repair, got success")
			}
			if !strings.Contains(q, "```tool block") || !strings.Contains(q, "tool: echo") {
				t.Errorf("repair must restate the block shape: %q", q)
			}
		})
	}
}

func TestNativeTypeRepairRefersToAdvertisedArgumentsWithoutCallCount(t *testing.T) {
	d := Definition{Name: "echo", Params: []Param{{Name: "n", Type: TypeInt, Required: true}}}
	_, repair := validate(d, nil, map[string]any{"n": "not an integer"})
	if repair == "" || !strings.Contains(repair, "advertised schema") || strings.Contains(repair, "one corrected function call") {
		t.Fatalf("native repair adds a contradictory call-count instruction: %q", repair)
	}
}
