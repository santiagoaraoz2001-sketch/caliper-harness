package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"specific-harness/internal/decision"
)

var styleOptions = []string{
	"approve the completed code as consistent with the styleguide",
	"send the completed code back for adjustment to the styleguide",
}

// styleSendback asks the decision judge to compare a validated task's
// reported result with the workspace styleguide. It returns the guidance
// to inject on a send-back verdict, or "" to proceed. The judge is
// advisory here like everywhere else: unavailable, low-confidence and
// unsupported verdicts never block a validated handoff, and at most one
// send-back round runs per Run.
func (r *Runner) styleSendback(ctx context.Context, final string, log func(string, string) error) string {
	if !r.StyleEnforcement || r.Styleguide == "" || r.Judge == nil || r.styleRounds >= 1 {
		return ""
	}
	input := struct {
		Styleguide   string
		FinalAnswer  string
		TaskID       string
		Verification string
	}{r.Styleguide, final, "", ""}
	if s := r.settlement; s != nil {
		input.TaskID, input.Verification = s.TaskID, string(s.Verification)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	req := decision.Request{Context: string(raw), Instructions: r.Prompts.Text("decision.style.instructions"), Options: styleOptions}
	v, err := r.Judge(ctx, req)
	if err != nil {
		_ = log("judgment-error", fmt.Sprintf("source=styleguide error=%q", err))
		return ""
	}
	resolution := "approved"
	if v.Option == styleOptions[1] && v.Confidence >= advisoryConfidence {
		resolution = "send-back"
	} else if v.Confidence < advisoryConfidence {
		resolution = "failed-confidence"
	} else if v.Option != styleOptions[0] {
		resolution = "unsupported-option"
	}
	decision.LogJudgment(func(kind, text string) { _ = log(kind, text) }, "styleguide", req, v, resolution, r.JudgmentContextChars)
	if resolution != "send-back" {
		return ""
	}
	r.styleRounds++
	return r.Prompts.Text("continuation.style_sendback") + "\n" + r.Styleguide
}
