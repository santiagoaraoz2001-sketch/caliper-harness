package prompts

import (
	"strings"
	"testing"
)

func TestAutonomousFeatureGuidanceListsToolsAndTriggers(t *testing.T) {
	pc := Builtin()
	cases := map[string][]string{
		"system.features.plan":      {"plan({})", "major work", "several connected files", "direct coding", "localized"},
		"system.features.elaborate": {"elaborate", "purpose", "inputs", "completion evidence", "risks", "Chat prose is not approval"},
		"system.features.library":   {"build_block", "build_workflow", "library_workflow", "skill workflow", "major/high-complexity", "PAT owns", "attach", "search", "local adaptation", "unverified"},
		"system.features.wait":      {"wait", "process", "file", "HTTP", "still_waiting", "shell sleep"},
	}
	for key, wants := range cases {
		text := pc.Text(key)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q: %s", key, want, text)
			}
		}
	}
	if heading := pc.Text("system.features.heading"); !strings.Contains(heading, "currently advertised") {
		t.Fatalf("feature heading does not limit guidance to the current tool view: %s", heading)
	}
	for _, key := range []string{"tools.elaborate.description", "tools.elaborate.purpose", "help.elaborate"} {
		if strings.TrimSpace(pc.Text(key)) == "" || strings.Contains(pc.Text(key), "missing prompt") {
			t.Errorf("%s is not defined", key)
		}
	}
}
