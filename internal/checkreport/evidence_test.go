package checkreport

import (
	"encoding/json"
	"strings"
	"testing"
)

func evidenceFixture() (Contract, PrerequisiteReport, string) {
	prerequisite := Prerequisite{Kind: "path", Value: "browser-fixture"}
	contract := Contract{Format: EvidenceFormat, Expected: []string{"unit", "browser"}, Stages: []StageRequirement{{ID: "browser", Prerequisites: []Prerequisite{prerequisite}, Evidence: []EvidenceRequirement{{ID: "visible-filter", Expected: "Active filter preserves A and hides completed B"}}}}}
	host := PrerequisiteReport{RunID: "current", Workspace: "/owned", Observations: []PrerequisiteObservation{{StageID: "browser", Prerequisite: prerequisite, Status: "present"}}}
	report := `{"format":"caliper-test-report-v2","run_id":"current","stages":[{"id":"unit","status":"passed","discovered":2,"executed":2,"failed":0,"skipped":0},{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0,"evidence":[{"id":"visible-filter","observed":"Clicked Active: A visible, completed B absent","reference":"browser case filter-visible"}]}]}`
	return contract, host, report
}

func TestEvidenceContractCurrentObservations(t *testing.T) {
	c, host, valid := evidenceFixture()
	for _, tc := range []struct {
		name   string
		change func(string) string
	}{
		{"valid", func(s string) string { return s }},
		{"wrong-run", func(s string) string { return strings.Replace(s, `"current"`, `"old"`, 1) }},
		{"wrong-stage", func(s string) string { return strings.Replace(s, `"browser"`, `"other"`, 1) }},
		{"missing-evidence", func(s string) string { return strings.Replace(s, `"visible-filter"`, `"unrelated"`, 1) }},
		{"empty-observation", func(s string) string {
			return strings.Replace(s, `Clicked Active: A visible, completed B absent`, ` `, 1)
		}},
		{"duplicate-evidence", func(s string) string {
			return strings.Replace(s, `"evidence":[`, `"evidence":[{"id":"visible-filter","observed":"duplicate"},`, 1)
		}},
		{"duplicate-stage", func(s string) string { return strings.Replace(s, `"browser"`, `"unit"`, 1) }},
		{"exit-zero-skip", func(s string) string {
			return strings.Replace(s, `"status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0`, `"status":"skipped","discovered":1,"executed":0,"failed":0,"skipped":1`, 1)
		}},
		{"zero-tests", func(s string) string {
			return strings.Replace(s, `"discovered":1,"executed":1`, `"discovered":0,"executed":0`, 1)
		}},
		{"malformed", func(s string) string { return s[:len(s)-1] }},
		{"unsupported-version", func(s string) string { return strings.Replace(s, EvidenceFormat, "caliper-test-report-v3", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(c, "checker", "current", tc.change(valid), false, "", "", host)
			if got.Satisfied() != (tc.name == "valid") {
				t.Fatalf("unexpected qualification: %+v", got)
			}
		})
	}
	if got := Evaluate(c, "checker", "current", valid, true, "", "", host); got.Satisfied() {
		t.Fatal("truncated current output qualified")
	}
	if got := Evaluate(c, "checker", "current", valid, false, "", ""); got.Status != "unknown" {
		t.Fatalf("producer report replaced host prerequisite observations: %+v", got)
	}
	for _, kind := range []string{"missing", "unknown", "wrong-run", "wrong-workspace", "duplicate", "absent", "manual-present"} {
		t.Run(kind, func(t *testing.T) {
			c, h, report := evidenceFixture()
			switch kind {
			case "wrong-run":
				h.RunID = "old"
			case "wrong-workspace":
				h.Workspace = "/elsewhere"
				if h.Validate(c, "current", "/owned") == nil {
					t.Fatal("workspace binding absent")
				}
				return
			case "duplicate":
				h.Observations = append(h.Observations, h.Observations[0])
			case "absent":
				h.Observations = nil
			case "manual-present":
				c.Stages[0].Prerequisites[0].Kind = "manual"
				h.Observations[0].Prerequisite.Kind = "manual"
			default:
				h.Observations[0].Status = kind
			}
			if got := Evaluate(c, "checker", "current", report, false, "", "", h); got.Satisfied() {
				t.Fatalf("unverified prerequisite qualified: %+v", got)
			}
		})
	}
}

func TestEvidenceContractValidationAndFreeze(t *testing.T) {
	c, _, _ := evidenceFixture()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	frozen := c.Clone()
	c.Stages[0].Prerequisites[0].Value = "changed"
	c.Stages[0].Evidence[0].Expected = "weakened"
	if c.Equal(frozen) || frozen.Stages[0].Prerequisites[0].Value != "browser-fixture" || frozen.Stages[0].Evidence[0].Expected == "weakened" {
		t.Fatal("nested contract aliases live config")
	}
	for _, format := range []string{"", StageFormat, "go-test-json"} {
		legacy := frozen.Clone()
		legacy.Format = format
		if legacy.Validate() == nil {
			t.Fatalf("rich requirements silently accepted by %q", format)
		}
	}
	for _, mutate := range []func(*Contract){
		func(c *Contract) { c.Stages = append(c.Stages, c.Stages[0]) },
		func(c *Contract) { c.Stages[0].ID = "undeclared" },
		func(c *Contract) {
			c.Stages[0].Prerequisites = append(c.Stages[0].Prerequisites, c.Stages[0].Prerequisites[0])
		},
		func(c *Contract) { c.Stages[0].Prerequisites[0].Kind = "command" },
		func(c *Contract) { c.Stages[0].Evidence = append(c.Stages[0].Evidence, c.Stages[0].Evidence[0]) },
		func(c *Contract) { c.Stages[0].Evidence[0].Expected = "" },
	} {
		bad := frozen.Clone()
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatalf("ambiguous contract admitted: %+v", bad)
		}
	}
	raw, err := json.Marshal(frozen)
	var restored Contract
	if err != nil || json.Unmarshal(raw, &restored) != nil || !restored.Equal(frozen) {
		t.Fatal("contract archive lost rich requirements")
	}
}
