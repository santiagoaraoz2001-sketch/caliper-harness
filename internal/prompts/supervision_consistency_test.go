package prompts

import (
	"strings"
	"testing"
)

func TestThinkingDirectionPromptCannotTerminateOrRestartReasoning(t *testing.T) {
	text := strings.ToLower(Builtin().Text("decision.thinking_loop.instructions"))
	for _, incompatible := range []string{"otherwise stop", "stop the run", "discard", "restart the response"} {
		if strings.Contains(text, incompatible) {
			t.Errorf("thinking direction contradicts advisory supervision: %q in %q", incompatible, text)
		}
	}
	for _, necessary := range []string{"continue", "reconsider", "nudge", "length"} {
		if !strings.Contains(text, necessary) {
			t.Errorf("thinking advice omits %q", necessary)
		}
	}
}

func TestTaskAuthoringKeepsRequestedBehaviorAndItsAssertionsTogether(t *testing.T) {
	for _, key := range []string{"planning.task", "planning.proof", "decision.readiness.instructions", "tools.workflow_task.purpose"} {
		text := strings.ToLower(Builtin().Text(key))
		if !strings.Contains(text, "assertions") || !strings.Contains(text, "future task") {
			t.Errorf("%s still permits the behavior task to depend on a future assertion task: %s", key, text)
		}
	}
}

func TestNewDeliverableRoleAndAdviceDoNotRequireAnOriginalBug(t *testing.T) {
	for _, key := range []string{"system.coding.direct", "templates.review", "progress.execute"} {
		text := Builtin().texts[key]
		for _, branch := range []string{"reported defect", "new deliverable"} {
			if !strings.Contains(text, branch) {
				t.Errorf("%s lacks the %q branch; new work can be mistaken for a defect: %s", key, branch, text)
			}
		}
	}
}

func TestNativeRepairKeepsValidatedArrayProtocol(t *testing.T) {
	for _, key := range []string{"repair.arguments.required_native", "repair.arguments.unknown_native", "repair.native.corrected_call_suffix", "repair.native.text_syntax"} {
		text := strings.ToLower(Builtin().texts[key])
		if strings.Contains(text, "one corrected function call") || strings.Contains(text, "one structured function call") {
			t.Errorf("%s contradicts the valid native call-array protocol: %s", key, text)
		}
	}
	// Text compatibility retains its independently defined one-block protocol.
	if !strings.Contains(Builtin().Text("repair.text.multiple_calls"), "one tool block") {
		t.Fatal("native repair changed the text compatibility protocol")
	}
}
