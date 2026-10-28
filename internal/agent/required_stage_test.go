package agent

import (
	"encoding/json"
	"os"
	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/process"
	"strings"
	"testing"
)

// The browser stage can be skipped while the wrapper exits zero. A current
// execution receipt must retain that fact and must not qualify the task.
func TestRequiredStagesBindCurrentExecutionAndRefuseSkippedBrowser(t *testing.T) {
	t.Setenv(process.TestRunVariable, "inherited-not-authority")
	for _, paired := range []bool{false, true} {
		for _, status := range []string{"passed", "skipped", "replayed", "opaque"} {
			t.Run(status+map[bool]string{true: "-assigned", false: "-ordinary"}[paired], func(t *testing.T) {
				stage := `{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0}`
				if status == "skipped" {
					stage = `{"id":"browser","status":"skipped","discovered":1,"executed":0,"failed":0,"skipped":1}`
				}
				id := `"$CALIPER_TEST_RUN_ID"`
				if status == "replayed" {
					id = `old-run`
				}
				command := `printf '{"format":"caliper-test-report-v1","run_id":"%s","stages":[` + stage + `]}' ` + id
				contract := checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"browser"}}
				if status == "opaque" {
					contract = checkreport.Contract{}
					command = "printf 'SKIP browser'"
				}
				root := t.TempDir()
				c := &session.CodingState{TaskID: "fixture", Workspace: root, Command: command, ConfigCommand: command, Report: contract.Clone()}
				if paired {
					c.Attempt = 1
					c.AcceptanceCommand = "printf accepted"
				}
				r := &Runner{Prompts: prompts.Builtin(), CheckCommand: command, CheckReport: contract.Clone(), Session: &recordSink{}, SessionID: "required-report", Continuation: session.Continuation{Coding: c}}
				def := r.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, root))
				raw, _ := json.Marshal(map[string]string{"cmd": command, "acceptance_cmd": c.AcceptanceCommand})
				args, repair := tool.ValidateJSON(def, string(raw))
				if repair != "" && !paired {
					raw, _ = json.Marshal(map[string]string{"cmd": command})
					args, repair = tool.ValidateJSON(def, string(raw))
				}
				if repair != "" {
					t.Fatal(repair)
				}
				result, err := Direct(t.Context(), def, args)
				if err != nil || c.Receipt == nil || c.Receipt.ExitCode == nil || *c.Receipt.ExitCode != 0 {
					t.Fatalf("actual wrapper did not exit zero: %+v %v", result, err)
				}
				want := status == "passed" || status == "opaque"
				if c.Passed() != want {
					t.Fatalf("qualification got %t wanted %t: %+v", c.Passed(), want, c.Receipt)
				}
				if c.Receipt.RunID == "" || c.Receipt.RunID == "inherited-not-authority" || os.Getenv(process.TestRunVariable) != "inherited-not-authority" {
					t.Fatal("host run identity leaked or inherited")
				}
				if status == "passed" && !strings.Contains(c.Receipt.Text, c.Receipt.RunID) {
					t.Fatal("child did not receive owning execution ID")
				}
				if status == "opaque" && c.Receipt.Coverage.Status != "unknown" {
					t.Fatal("opaque process pass invented coverage")
				}
				if status == "skipped" && (c.Receipt.Coverage.Skipped != 1 || c.Receipt.Status == "passed") {
					t.Fatal("required skip qualified")
				}
				// Changing the declared contract cannot adopt even a real completed receipt.
				if status == "passed" {
					c.Report.Expected = []string{"different"}
					if c.Passed() {
						t.Fatal("changed contract adopted old success")
					}
				}
			})
		}
	}
}
