package launch

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
)

func TestReviewProjectionUsesAllOwnedInputsAndKeepsUnknownTemplates(t *testing.T) {
	fields := map[string]string{}
	for _, key := range []string{"PlanGoal", "ApproachTitle", "Title", "Prompt", "Current", "TaskPrompt", "TaskCurrent", "Target", "Sketch", "Tests", "Acceptance", "Workspace", "CheckCommand", "AcceptanceCommand", "ExecutionRequest", "ExecutionEvidence"} {
		fields[key] = key + ": unique café requirement/caveat"
	}
	fields["FileObservations"] = `[{"CallID":"edit-1","Tool":"edit","Result":"refused; partial effect unknown"}]`
	fields["CheckReceipt"] = `{"task_id":"task","attempt":2,"receipt":{"status":"failed"}}`
	prompt, err := prompts.Builtin().Render("templates.review", fields)
	if err != nil {
		t.Fatal(err)
	}
	inputs := reviewProgressInputs(prompt, fields)
	if inputs == nil {
		t.Fatal("known typed input not projected")
	}
	raw, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if key == "CheckReceipt" || key == "FileObservations" {
			continue
		}
		if !strings.Contains(string(raw), value) {
			t.Errorf("unique %s lost", key)
		}
	}
	if string(inputs.FileObservations) != fields["FileObservations"] || string(inputs.InitialCheck) != fields["CheckReceipt"] {
		t.Fatal("raw evidence changed")
	}
	for _, want := range []string{"Source, DOM data and screenshot paths are not rendered visual inspection", "Report UNVERIFIED", "Previews may be partial", "worker report (unverified)"} {
		if !strings.Contains(inputs.Instructions, want) {
			t.Errorf("fixed review obligation lost: %s", want)
		}
	}
	for _, unknown := range []string{prompt + "\nCUSTOM: inspect the unusual runtime contract", "restored/historical human review request"} {
		if reviewProgressInputs(unknown, fields) != nil {
			t.Fatal("unknown template was silently decomposed")
		}
	}
	fields["FileObservations"] = "unknown non-JSON retained evidence"
	if reviewProgressInputs(prompt, fields) != nil {
		t.Fatal("unknown evidence shape was decomposed")
	}
}
