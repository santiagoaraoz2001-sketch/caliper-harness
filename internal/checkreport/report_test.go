package checkreport

import (
	"fmt"
	"strings"
	"testing"
)

func TestDeclaredStageReportRejectsFalseSuccess(t *testing.T) {
	c := Contract{Format: StageFormat, Expected: []string{"unit", "browser"}}
	valid := `{"format":"caliper-test-report-v1","run_id":"current","stages":[{"id":"unit","status":"passed","discovered":2,"executed":2,"failed":0,"skipped":0},{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0}]}`
	skip := strings.Replace(valid, `"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0`, `"id":"browser","status":"skipped","discovered":1,"executed":0,"failed":0,"skipped":1`, 1)
	fail := strings.Replace(valid, `"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0`, `"id":"browser","status":"failed","discovered":1,"executed":1,"failed":1`, 1)
	for _, tc := range []struct {
		name, text string
		truncated  bool
		pass       bool
	}{
		{"valid", valid, false, true}, {"browser exit-zero skip", skip, false, false}, {"failed exit-zero", fail, false, false},
		{"replay", strings.Replace(valid, `"current"`, `"old"`, 1), false, false},
		{"partial", valid[:len(valid)-1], false, false}, {"clipped", valid, true, false},
		{"missing-browser", strings.Replace(valid, `"browser"`, `"other"`, 1), false, false},
		{"duplicate-stage", strings.Replace(valid, `"browser"`, `"unit"`, 1), false, false},
		{"zero-executed", strings.Replace(valid, `"executed":1`, `"executed":0`, 1), false, false},
		{"contradiction", strings.Replace(valid, `"failed":0`, `"failed":3`, 1), false, false},
		{"duplicate-key", strings.Replace(valid, `"run_id":"current"`, `"run_id":"old","run_id":"current"`, 1), false, false},
		{"second-envelope", valid + valid, false, false}, {"logged-prefix", "PASS\n" + valid, false, false},
		{"missing", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(c, "node checker.js", "current", tc.text, tc.truncated, "", "")
			if got.Satisfied() != tc.pass {
				t.Fatalf("got %+v", got)
			}
			if tc.name == "failed exit-zero" && got.Failed != 1 {
				t.Fatal("failed stage was erased")
			}
			if tc.name == "browser exit-zero skip" && got.Skipped != 1 {
				t.Fatal("required skip was erased")
			}
		})
	}
	optional := strings.TrimSuffix(valid, "]}") + `,{"id":"optional","status":"skipped","discovered":1,"executed":0,"failed":0,"skipped":1}]}`
	if got := Evaluate(c, "checker", "current", optional, false, "", ""); !got.Satisfied() || got.Skipped != 1 {
		t.Fatalf("optional skip changed required scope or vanished: %+v", got)
	}
	huge := fmt.Sprintf(`{"format":"caliper-test-report-v1","run_id":"current","stages":[{"id":"unit","status":"passed","discovered":%d,"executed":%d},{"id":"browser","status":"passed","discovered":%d,"executed":%d}]}`, int(^uint(0)>>1), int(^uint(0)>>1), int(^uint(0)>>1), int(^uint(0)>>1))
	if got := Evaluate(c, "checker", "current", huge, false, "", ""); got.Satisfied() {
		t.Fatalf("overflow qualified: %+v", got)
	}

	if Evaluate(Contract{}, "anything", "", "SKIP browser", false, "", "").Status != "unknown" {
		t.Fatal("arbitrary log text became coverage")
	}
	for _, cmd := range []string{"go test -json -count=1; cat old.json", "go test -json -count=1 | cat", "go test -json -count=1 $(cat old)", "go test -json -count=1 -count=2"} {
		if UncachedGoTest(cmd) {
			t.Fatalf("shell wrapper admitted: %s", cmd)
		}
	}
}
