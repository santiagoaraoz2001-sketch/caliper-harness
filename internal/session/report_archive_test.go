package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/llm"
)

func TestReportArchiveZeroPreservesLegacyBytes(t *testing.T) {
	const legacy = `{"task_id":"legacy","workspace":"/owned","command":"suite","revision":1,"receipt":{"command":"suite","cwd":"/owned","revision":1,"started":"start","ended":"end","exit_code":0,"status":"passed"}}`
	for _, expected := range [][]string{nil, {}} {
		var state CodingState
		if err := json.Unmarshal([]byte(legacy), &state); err != nil {
			t.Fatal(err)
		}
		state.Report.Expected = expected
		state.Receipt.Report.Expected = expected
		encoded, err := json.Marshal(state)
		if err != nil || string(encoded) != legacy {
			t.Fatalf("zero metadata changed legacy bytes: %s %v", encoded, err)
		}
		if err := state.Validate(); err != nil || state.Passed() {
			t.Fatalf("legacy evidence changed qualification: %+v %v", state, err)
		}
	}
}

func TestReportArchiveMeaningfulRoundTrip(t *testing.T) {
	contract := checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"render-α", "consumer"}}
	coverage := checkreport.Coverage{Status: "matched", Expected: 2, Discovered: 2, Executed: 2, Passed: 2}
	zero := 0
	state := &CodingState{Report: contract, TaskID: "direct", Workspace: "/owned", Command: "suite", Revision: 1,
		Receipt: &CheckReceipt{Report: contract.Clone(), RunID: "actual-run", Coverage: coverage, Direct: true,
			Command: "suite", Cwd: "/owned", Revision: 1, Started: "start", Ended: "end", EpisodeStarted: "start", EpisodeEnded: "end", ExitCode: &zero, Status: "passed"}}
	continuation := Continuation{Version: 1, Entries: []Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "Keep the current requirement"}}}, Coding: state}
	encoded, err := continuation.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Continuation
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || !reflect.DeepEqual(decoded.Coding, state) {
		t.Fatalf("meaningful evidence lost: %+v %v", decoded.Coding, err)
	}
	restored, _, err := Restore([]Event{{Kind: "continuation", Text: encoded}}, 32768)
	if err != nil || restored.Coding == nil || !restored.Coding.Report.Equal(contract) || restored.Coding.Receipt.RunID != "actual-run" || restored.Coding.Receipt.Coverage != coverage {
		t.Fatalf("archive restoration lost report facts: %+v %v", restored.Coding, err)
	}
	if restored.Coding.Passed() || restored.Coding.Receipt.Status != "historical" {
		t.Fatal("restored report was promoted to current proof")
	}
}

func TestReportArchiveDoesNotHideMalformedFacts(t *testing.T) {
	for _, contract := range []checkreport.Contract{{Expected: []string{"orphan-stage"}}, {Format: "unknown-format"}, {Format: checkreport.StageFormat}} {
		state := CodingState{TaskID: "task", Workspace: "/owned", Report: contract}
		encoded, err := json.Marshal(state)
		var decoded CodingState
		if err != nil || !strings.Contains(string(encoded), `"report":`) || json.Unmarshal(encoded, &decoded) != nil || !decoded.Report.Equal(contract) || decoded.Validate() == nil {
			t.Fatalf("malformed contract silently omitted or normalized: %s %v", encoded, err)
		}
	}
	for _, coverage := range []checkreport.Coverage{{Reason: "unverified source"}, {Expected: -1}, {Status: "unknown"}} {
		receipt := CheckReceipt{Coverage: coverage}
		encoded, err := json.Marshal(receipt)
		var decoded CheckReceipt
		if err != nil || !strings.Contains(string(encoded), `"coverage":`) || json.Unmarshal(encoded, &decoded) != nil || decoded.Coverage != coverage {
			t.Fatalf("nonzero coverage silently omitted or normalized: %s %v", encoded, err)
		}
	}
}

func TestReportArchiveRichPrerequisitesRemainOwned(t *testing.T) {
	prerequisite := checkreport.Prerequisite{Kind: "path", Value: "browser"}
	contract := checkreport.Contract{Format: checkreport.EvidenceFormat, Expected: []string{"consumer"}, Stages: []checkreport.StageRequirement{{ID: "consumer", Prerequisites: []checkreport.Prerequisite{prerequisite}, Evidence: []checkreport.EvidenceRequirement{{ID: "public", Expected: "actual public result"}}}}}
	zero := 0
	state := CodingState{Report: contract, TaskID: "direct", Workspace: "/owned", Command: "suite", Revision: 1,
		Receipt: &CheckReceipt{Report: contract.Clone(), RunID: "current", Coverage: checkreport.Coverage{Status: "matched"}, Direct: true,
			Command: "suite", Cwd: "/owned", Revision: 1, Started: "start", Ended: "end", EpisodeStarted: "start", EpisodeEnded: "end", ExitCode: &zero, Status: "passed"}}
	if state.Passed() || state.Validate() == nil {
		t.Fatal("coverage label substituted for missing host observations")
	}
	state.Receipt.Prerequisites = &checkreport.PrerequisiteReport{RunID: "current", Workspace: "/owned", Observations: []checkreport.PrerequisiteObservation{{StageID: "consumer", Prerequisite: prerequisite, Status: "present"}}}
	if !state.Passed() || state.Validate() != nil {
		t.Fatal("owned observations rejected")
	}
	raw, err := json.Marshal(state)
	var restored CodingState
	if err != nil || json.Unmarshal(raw, &restored) != nil || !reflect.DeepEqual(restored, state) || !restored.Passed() {
		t.Fatal("rich receipt lost on round trip")
	}
	restored.Receipt.Prerequisites.Workspace = "/elsewhere"
	if restored.Passed() || restored.Validate() == nil {
		t.Fatal("other workspace prerequisite report qualified")
	}
	restored.Receipt.Prerequisites.Workspace = "/owned"
	restored.Receipt.Prerequisites.RunID = "old"
	if restored.Passed() {
		t.Fatal("prior episode prerequisite report qualified")
	}
}
