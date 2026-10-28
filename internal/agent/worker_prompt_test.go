package agent

import (
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestCodingPromptRequiresLiveWorkerProvenance(t *testing.T) {
	pc := prompts.Builtin()
	for _, tc := range []struct {
		name                  string
		role, bound, coding   bool
		wantDirect, wantBound bool
	}{
		{"ordinary coding", false, false, true, true, false},
		{"historical coding state alone", false, true, true, true, false},
		{"role probe without live attempt", true, false, true, true, false},
		{"claimed worker", true, true, true, false, true},
		{"noncoding role", true, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{Prompts: pc, Registry: tool.NewRegistry(pc), CodingRequired: tc.coding}
			if tc.role {
				r.TaskSystem = "Host-installed frozen role"
			}
			if tc.bound {
				r.Continuation.Coding = &session.CodingState{TaskID: "owned-task", Attempt: 1}
			}
			text := r.basePrompt()
			if strings.Contains(text, pc.Text("system.coding.direct")) != tc.wantDirect || strings.Contains(text, pc.Text("system.coding.requirements")) != tc.wantBound {
				t.Fatal("coding prompt role did not match live host provenance")
			}
			wantKnowledge := 0
			if tc.coding {
				wantKnowledge = 1
			}
			if strings.Count(text, pc.Text("system.coding.knowledge")) != wantKnowledge {
				t.Fatal("knowledge instructions duplicated or invented for noncoding role")
			}
		})
	}
}
