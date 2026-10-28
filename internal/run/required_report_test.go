package run

import (
	"context"
	"specific-harness/internal/checkreport"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestOptionalReportsCannotDischargeConfiguredRequiredStage(t *testing.T) {
	d, _ := testManagementFixture(t)
	command := func(id, status string) string {
		counts := `"discovered":1,"executed":1,"failed":0,"skipped":0`
		if status == "skipped" {
			counts = `"discovered":1,"executed":0,"failed":0,"skipped":1`
		}
		return `printf '{"format":"caliper-test-report-v1","run_id":"%s","stages":[{"id":"` + id + `","status":"` + status + `",` + counts + `}]}' "$CALIPER_TEST_RUN_ID"`
	}
	d.Cfg.Coding.CheckCommand = command("browser", "skipped")
	d.Cfg.Coding.ReportFormat = checkreport.StageFormat
	d.Cfg.Coding.ExpectedTests = []string{"browser"}
	d.Cfg.Testing.Suites[0].Command = command("optional", "passed")
	d.Cfg.Testing.Suites[0].ReportFormat = checkreport.StageFormat
	d.Cfg.Testing.Suites[0].ExpectedTests = []string{"optional"}
	optional := runOptional(t, d, d.Cfg.Testing.Suites[0].ID)
	if optional.Status != "passed" || optional.Results[0].Coverage.Status != "matched" {
		t.Fatalf("optional positive failed: %+v", optional)
	}
	required := runOptional(t, d, ConfiguredCheckID)
	if required.Status == "passed" || required.Results[0].Coverage.Skipped != 1 || !strings.Contains(required.Results[0].Reason, "required stage") {
		t.Fatalf("required browser skip accepted: %+v", required)
	}
	if optional.Results[0].Receipt.RunID == required.Results[0].Receipt.RunID {
		t.Fatal("separate owners reused execution identity")
	}
	events, err := session.Read(d.Cfg.Sessions.Path, optional.Selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := session.Restore(events, 32768)
	if c.Coding != nil {
		t.Fatal("standalone report became conversation qualification")
	}
	// Optional enable/disable never changes the required configured obligation.
	d.Cfg.Testing.Suites[0].Enabled = false
	d.Cfg.Testing.Suites[0].DisabledReason = "not selected"
	if rows := TestSuites(d.Cfg); !rows[0].Required || rows[0].ExpectedTests[0] != "browser" {
		t.Fatal("optional switch changed required contract")
	}
}

func TestRequiredEvidenceSelectionFreezesNestedStages(t *testing.T) {
	d, _ := testManagementFixture(t)
	d.Cfg.Coding.CheckCommand = "required-check"
	d.Cfg.Coding.ReportFormat, d.Cfg.Coding.ExpectedTests = checkreport.EvidenceFormat, []string{"required"}
	d.Cfg.Coding.Stages = []checkreport.StageRequirement{{ID: "required", Evidence: []checkreport.EvidenceRequirement{{ID: "public-output", Expected: "Current public output"}}}}
	wanted := d.Cfg.Coding.ReportContract()
	selection, err := FreezeTests(context.Background(), d, []string{ConfiguredCheckID})
	if err != nil {
		t.Fatal(err)
	}
	d.Cfg.Coding.Stages[0].Evidence[0].Expected = "weaker"
	if !selection.Suites[0].ReportContract().Equal(wanted) {
		t.Fatal("frozen required selection aliases mutable config")
	}
	if selectionHash(selection) != selection.Hash {
		t.Fatal("captured selection mutated after config change")
	}
	changed, err := FreezeTests(context.Background(), d, []string{ConfiguredCheckID})
	if err != nil || changed.Hash == selection.Hash {
		t.Fatalf("rich contract absent from selection identity: %v", err)
	}
}

func TestRequiredEvidenceReadinessIncludesStagePrerequisites(t *testing.T) {
	for _, kind := range []string{"path", "manual"} {
		for _, required := range []bool{true, false} {
			d, _ := testManagementFixture(t)
			stages := []checkreport.StageRequirement{{ID: "consumer", Prerequisites: []checkreport.Prerequisite{{Kind: kind, Value: "not-observed"}}}}
			d.Cfg.Coding.CheckCommand, d.Cfg.Coding.ReportFormat, d.Cfg.Coding.ExpectedTests, d.Cfg.Coding.Stages = "required-check", checkreport.EvidenceFormat, []string{"consumer"}, stages
			optional := &d.Cfg.Testing.Suites[0]
			optional.ReportFormat, optional.ExpectedTests, optional.Stages = checkreport.EvidenceFormat, []string{"consumer"}, stages
			row := TestSuites(d.Cfg)[0]
			if !required {
				row = *optional
			}
			ready, reason := TestReadiness(context.Background(), d, row)
			if ready || !strings.Contains(reason, "stage consumer prerequisite") || !strings.Contains(reason, "check can run") {
				t.Fatalf("required=%v kind=%s falsely ready: %s", required, kind, reason)
			}
		}
	}
}
