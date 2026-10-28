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

func TestNativeSearchArgumentsDoNotBecomeBashEffects(t *testing.T) {
	d := fixtureTool()
	d.Purpose = strings.Repeat("Unrelated Git, Python and service guidance. ", 100)
	for _, raw := range []string{
		`{"command":"grep","pattern":"^export (function|const|class)"}`,
		`{"command":"grep","path":"src/core/world.mjs","pattern":"const SAFE|place\\(|wreck|asteroid|debris"}`,
	} {
		args, repair := ValidateJSON(d, raw)
		if args != nil || !strings.Contains(repair, "pattern") || !strings.Contains(repair, "from bash") {
			t.Fatalf("invalid Bash call must stay refused, not become search: %v %s", args, repair)
		}
		if strings.Contains(raw, `"path"`) && !strings.Contains(repair, `"path", "pattern"`) {
			t.Fatal("repair must name all invalid fields together", repair)
		}
		if len(repair) > 600 || strings.Contains(repair, d.Purpose) || strings.Count(repair, "Current arguments:") != 1 {
			t.Fatalf("repair should contain one bounded argument correction, not repeat full help (%d bytes): %s", len(repair), repair)
		}
	}
	// Existing compatibility is still definition-owned; conflicting aliases stay refused.
	if args, repair := ValidateJSON(d, `{"command":"echo ok"}`); repair != "" || args["cmd"] != "echo ok" {
		t.Fatalf("command alias changed: %v %s", args, repair)
	}
	if args, repair := ValidateJSON(d, `{"command":"one","cmd":"two"}`); args != nil || repair == "" {
		t.Fatal("conflicting aliases must not dispatch", args, repair)
	}
}
