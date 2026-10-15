package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
)

// Recorded verdict confidences replayed at the actual Runner boundary; no inference.
func TestDiscoveryUncertainDirectionRecordedVerdicts(t *testing.T) {
	confidences := []float64{.404, .499, .5, .5892429224515646, .589449226521897, .674686344896878, .7508494126999069}
	for _, option := range []string{discoveryActOption, discoveryContinueOption} {
		for _, confidence := range confidences {
			t.Run(fmt.Sprintf("%s/%.15f", option, confidence), func(t *testing.T) {
				m := &discoveryModel{}
				for i := 0; i < 5; i++ {
					m.replies = append(m.replies, discoveryReply(fmt.Sprint(i)))
				}
				sink := &recordSink{}
				calls := 0
				r := discoveryRunner(t, m, func(context.Context, decision.Request) (decision.Verdict, error) {
					calls++
					return decision.Verdict{Option: option, Confidence: confidence}, nil
				}, sink)
				_, _, err := r.Run(context.Background(), "same bounded source task; retain approval and checks")
				if err != nil || calls != 1 || r.Outcome.Status != outcome.Completed || len(m.seen) != 6 {
					t.Fatalf("actual Runner boundary calls=%d outcome=%+v requests=%d error=%v", calls, r.Outcome, len(m.seen), err)
				}
				key, resolution := "continuation.discovery.continue", "continued-work"
				if option == discoveryActOption {
					key, resolution = "continuation.discovery.act", "repair-rejected-call"
				}
				if confidence < .5 {
					resolution = "failed-confidence"
				}
				direction := prompts.Builtin().Text(key)
				guidance := ""
				judgments := 0
				for i, kind := range sink.kinds {
					if kind == "guidance" {
						guidance = sink.texts[i]
					}
					if kind == "judgment" {
						j, ok := decision.ParseJudgment(sink.texts[i])
						if !ok || j.Source != "discovery-checkpoint" || j.Pick != option || j.Resolution != resolution {
							t.Fatalf("direction/uncertainty not honestly logged: %s", sink.texts[i])
						}
						judgments++
					}
					if kind == "tool" && !strings.HasPrefix(sink.texts[i], "read ") {
						t.Fatal("advice executed a tool or claimed authority", sink.texts[i])
					}
				}
				wantNudge := option == discoveryActOption && confidence >= .5
				if judgments != 1 || (guidance != "") != wantNudge || wantNudge && !strings.Contains(guidance, direction) {
					t.Fatalf("chosen direction ignored advisory floor/Continue silence: %q", guidance)
				}
				delivered := false
				for _, msg := range m.seen[5] {
					delivered = delivered || guidance != "" && msg.Role == "user" && msg.Content == guidance
				}
				if delivered != wantNudge {
					t.Fatal("NEXT model request ignored logged advisory disposition")
				}
				if r.assignedClaim || r.Continuation.Assignment != nil {
					t.Fatal("advice minted task claim")
				}
				for i := 0; i < 20; i++ {
					b, e := os.ReadFile(filepath.Join(r.Workspace, fmt.Sprint(i)))
					if e != nil || string(b) != fmt.Sprintf("actual source %d\nnext source line %d\n", i, i) {
						t.Fatal("source effect despite discovery-only replies", i, e)
					}
				}
				entries, e := os.ReadDir(r.Workspace)
				if e != nil || len(entries) != 20 {
					t.Fatal("unexpected file effect", len(entries), e)
				}
			})
		}
	}
}
