package config

import (
	"os"
	"path/filepath"
	"slices"
	"specific-harness/internal/checkreport"
	"testing"
)

func TestRequiredReportConfigPersistsSeparatelyFromOptionalSuites(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	c := Defaults()
	c.Coding = CodingConfig{CheckCommand: "node checker.mjs", ReportFormat: checkreport.StageFormat, ExpectedTests: []string{"unit", "browser"}}
	path := filepath.Join(t.TempDir(), "project.yaml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadProject(path)
	if err != nil || !loaded.Coding.ReportContract().Equal(c.Coding.ReportContract()) {
		t.Fatalf("required contract persistence: %+v %v", loaded.Coding, err)
	}
	original, _ := os.ReadFile(path)
	optional := sampleTestSuite()
	optional.ReportFormat = checkreport.StageFormat
	optional.ExpectedTests = []string{"optional"}
	c.Testing.Suites = []TestSuite{optional}
	c.Coding.ExpectedTests = []string{"replacement-not-owned-by-testing"}
	if err := SaveTesting(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = LoadProject(path)
	if err != nil || !slices.Equal(loaded.Coding.ExpectedTests, []string{"unit", "browser"}) {
		t.Fatalf("optional selection changed required stages: %+v %v", loaded.Coding, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) == string(original) {
		t.Fatal("optional test configuration did not persist")
	}
	for _, bad := range []CodingConfig{{ReportFormat: checkreport.StageFormat, ExpectedTests: []string{"browser"}}, {CheckCommand: "node checker", ReportFormat: checkreport.StageFormat}, {CheckCommand: "node checker", ReportFormat: checkreport.StageFormat, ExpectedTests: []string{"same", "same"}}, {CheckCommand: "node checker", ReportFormat: "text-skip-scan"}} {
		next := Defaults()
		next.Coding = bad
		if next.Validate() == nil {
			t.Fatalf("invalid contract admitted: %+v", bad)
		}
	}
}

func TestRequiredEvidenceConfigRoundTripAndOptionalIsolation(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	c := Defaults()
	c.Coding = CodingConfig{CheckCommand: "checker", ReportFormat: checkreport.EvidenceFormat, ExpectedTests: []string{"consumer"}, Stages: []checkreport.StageRequirement{{ID: "consumer", Prerequisites: []checkreport.Prerequisite{{Kind: "executable", Value: "node"}}, Evidence: []checkreport.EvidenceRequirement{{ID: "visible-result", Expected: "User-visible result from the actual entry point"}}}}}
	wanted := c.Coding.ReportContract()
	path := filepath.Join(t.TempDir(), "project.yaml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	frozen := c.Coding.ReportContract()
	c.Coding.Stages[0].Evidence[0].Expected = "changed"
	if !frozen.Equal(wanted) {
		t.Fatal("required capture aliases configuration")
	}
	optional := sampleTestSuite()
	optional.ReportFormat, optional.ExpectedTests, optional.Stages = checkreport.EvidenceFormat, []string{"consumer"}, wanted.Clone().Stages
	c.Testing.Suites = []TestSuite{optional}
	if err := SaveTesting(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadProject(path)
	if err != nil || !loaded.Coding.ReportContract().Equal(wanted) || !loaded.Testing.Suites[0].ReportContract().Equal(wanted) {
		t.Fatalf("nested ownership or YAML lost: %+v %v", loaded, err)
	}
}
