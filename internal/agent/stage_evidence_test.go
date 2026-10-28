package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestStagePrerequisitesPreserveIndependentCheckAndCurrentEvidence(t *testing.T) {
	for _, mode := range []string{"pass", "missing", "manual", "behavior-failed", "suite-nonzero", "cancel-between", "sink-between", "changed-contract"} {
		t.Run(mode, func(t *testing.T) {
			r, _, sink, root := episodeRunner(t, "printf BEHAVIOR")
			files := map[string]string{
				"converter.sh": `printf '%s' "$1" | tr '[:lower:]' '[:upper:]'`,
				"app.sh":       `sh converter.sh "$@"`,
				"check.sh": `printf RAN > suite.txt
test "$(sh converter.sh abc)" = ABC || exit 1
value=$(sh app.sh galaxy)
test "$value" = GALAXY || exit 1
printf '{"format":"caliper-test-report-v2","run_id":"%s","stages":[{"id":"unit","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0},{"id":"cli","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0,"evidence":[{"id":"public-output","observed":"CLI returned %s","reference":"app.sh galaxy"}]}]}' "$CALIPER_TEST_RUN_ID" "$value"`,
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := r.Continuation.Coding
			c.Command, c.ConfigCommand, r.CheckCommand = "sh check.sh", "sh check.sh", "sh check.sh"
			contract := checkreport.Contract{Format: checkreport.EvidenceFormat, Expected: []string{"unit", "cli"}, Stages: []checkreport.StageRequirement{{ID: "cli", Prerequisites: []checkreport.Prerequisite{{Kind: "path", Value: "app.sh"}}, Evidence: []checkreport.EvidenceRequirement{{ID: "public-output", Expected: "Public CLI outputs uppercase GALAXY"}}}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "missing":
				contract.Stages[0].Prerequisites[0].Value = "missing"
			case "manual":
				contract.Stages[0].Prerequisites[0] = checkreport.Prerequisite{Kind: "manual", Value: "user confirmed readiness"}
			case "behavior-failed":
				c.AcceptanceCommand = "printf ACTUAL_FAILURE; exit 1"
			case "suite-nonzero":
				files["check.sh"] += "\nexit 3"
				if err := os.WriteFile(filepath.Join(root, "check.sh"), []byte(files["check.sh"]), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancel-between":
				sink.cancel = cancel
			case "sink-between":
				sink.fail = 2
			}
			r.CheckReport, c.Report = contract.Clone(), contract.Clone()
			if mode == "changed-contract" {
				r.CheckReport.Stages[0].Evidence[0].Expected = "replacement obligation"
			}
			def := r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, root))
			args, repair := tool.ValidateJSON(def, `{}`)
			if repair != "" {
				t.Fatal(repair)
			}
			res, err := def.Execute(ctx, args)
			_, marker := os.Stat(filepath.Join(root, "suite.txt"))
			stopped := mode == "cancel-between" || mode == "sink-between" || mode == "changed-contract"
			if stopped {
				if err == nil || !os.IsNotExist(marker) || c.Passed() {
					t.Fatalf("stopped check escaped: %+v %v", res, err)
				}
				return
			}
			if err != nil || marker != nil {
				t.Fatalf("configured check did not run: %+v %v %v", res, err, marker)
			}
			if c.Passed() != (mode == "pass") || res.Failed != (mode != "pass") {
				t.Fatalf("incorrect qualification %s: %+v %+v", mode, res, c)
			}
			if e := c.Validate(); e != nil {
				t.Fatal(e)
			}
			if c.Receipt.Prerequisites.RunID != c.Receipt.RunID || c.Receipt.Prerequisites.Workspace != root || !strings.Contains(c.Receipt.Text, "CLI returned GALAXY") {
				t.Fatal("current host/producer evidence not retained")
			}
			events, e := session.Read(sink.dir, "paired")
			if e != nil || len(events) != 4 || !strings.Contains(events[0].Text, `"prerequisites"`) {
				t.Fatalf("pre-effect host observations missing: %v %+v", e, events)
			}
			if mode == "pass" {
				cloned := r.Continuation.Clone()
				c.Receipt.Prerequisites.Observations[0].Status = "missing"
				c.Report.Stages[0].Evidence[0].Expected = "changed"
				if !cloned.Coding.Passed() || c.Passed() {
					t.Fatal("nested frozen receipt aliases live owner")
				}
				cloned.Coding.Invalidate("later effect")
				if cloned.Coding.Passed() {
					t.Fatal("stale evidence qualified")
				}
			}
		})
	}
}
