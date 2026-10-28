package launch

import (
	"encoding/json"
	"reflect"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
)

func TestAttemptReportArchiveOmissionAndRoundTrip(t *testing.T) {
	for _, record := range []attemptRecord{
		{Kind: "execution", Attempt: 1},
		{Kind: "execution", Attempt: 1, Report: checkreport.Contract{Expected: []string{}}},
		{Kind: "check", Attempt: 1, Report: checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"consumer"}}, Coverage: checkreport.Coverage{Status: "matched", Expected: 1, Discovered: 1, Executed: 1, Passed: 1}},
		{Kind: "check", Attempt: 1, Report: checkreport.Contract{Expected: []string{"malformed-without-format"}}, Coverage: checkreport.Coverage{Reason: "unverified"}},
	} {
		encoded, err := json.Marshal(record)
		var fields map[string]json.RawMessage
		var decoded attemptRecord
		if err != nil || json.Unmarshal(encoded, &fields) != nil || json.Unmarshal(encoded, &decoded) != nil {
			t.Fatalf("record encoding failed: %s %v", encoded, err)
		}
		if _, present := fields["Report"]; present != !record.Report.IsZero() {
			t.Fatalf("wrong optional contract presence: %s", encoded)
		}
		if _, present := fields["Coverage"]; present != (record.Coverage != (checkreport.Coverage{})) {
			t.Fatalf("wrong optional coverage presence: %s", encoded)
		}
		if !decoded.Report.Equal(record.Report) || !reflect.DeepEqual(decoded.Coverage, record.Coverage) || decoded.Kind != record.Kind || decoded.Attempt != record.Attempt {
			t.Fatalf("record facts changed: %+v -> %+v", record, decoded)
		}
	}
}

func TestClaimKeepsExplicitEmptyReportContract(t *testing.T) {
	d, _, ids, root := roleHandoffFixture(t, 1, roleHandoffModel(func([]llm.Message) (string, error) { return "unused", nil }))
	owner := &Assigned{Deps: d, Root: root}
	if _, _, err := owner.Start(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	task, err := plan.GetTask(d.Store, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Kind   string
		Report json.RawMessage
	}
	if err := json.Unmarshal([]byte(task.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	contracts := 0
	for _, record := range records {
		if record.Kind == "execution_contract" {
			contracts++
			if string(record.Report) != "{}" {
				t.Fatalf("explicit admission contract omitted: %s", task.AttemptLog)
			}
		}
	}
	if contracts != 1 {
		t.Fatalf("wanted one immutable contract, got%d", contracts)
	}
}
