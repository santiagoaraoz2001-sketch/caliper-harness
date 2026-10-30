package prompts

import (
	"strings"
	"testing"
)

// The adapted ASD-STE100 clarity rule must reach every model role: the
// native orientation is in every structured system prompt and the text
// instructions are the text-mode equivalent. The judge capacity guidance
// must exist so size refusals never reuse the server-down text.
func TestClarityRuleReachesSystemPrompts(t *testing.T) {
	pc := Builtin()
	for _, key := range []string{"system.native.orientation", "system.text.instructions"} {
		text := strings.Join(strings.Fields(pc.Text(key)), " ")
		for _, want := range []string{
			"plain, direct language",
			"short sentences",
			"one term for one concept",
			"metaphors",
			"obsolete",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s omits clarity rule fragment %q", key, want)
			}
		}
	}
	capacity := strings.ToLower(strings.Join(strings.Fields(pc.Text("guidance.judge.capacity")), " "))
	for _, want := range []string{"too large", "shorten", "split", "server is not down"} {
		if !strings.Contains(capacity, want) {
			t.Errorf("guidance.judge.capacity omits %q", want)
		}
	}
	if strings.Contains(capacity, "/manage") {
		t.Error("capacity guidance must not send the model to /manage")
	}
	// Planning field anticipation observed live: the objective question must
	// withhold later fields explicitly.
	objective := strings.Join(strings.Fields(pc.Text("planning.step_objective")), " ")
	if !strings.Contains(objective, "Do not send execution or validation") {
		t.Error("planning.step_objective does not withhold later step fields")
	}
}
