package launch

import (
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"strings"
	"testing"
)

func TestSettledReviewerCommentLifecycle(t *testing.T) {
	for _, mode := range []string{config.SubagentsSequential, config.SubagentsConcurrent} {
		for _, pending := range []bool{false, true} {
			name := string(mode)
			if pending {
				name += "_pending_comment"
			}
			t.Run(name, func(t *testing.T) {
				var d Deps
				var ids []string
				var root string
				reviews := 0
				added := false
				model := roleHandoffModel(func(messages []llm.Message) (string, error) {
					reviewRequest := false
					for _, m := range messages {
						reviewRequest = reviewRequest || strings.HasPrefix(m.Content, "Review ")
					}
					if reviewRequest {
						reviews++
						actual, err := plan.GetTask(d.Store, ids[0])
						if err != nil {
							return "", err
						}
						if actual.State != plan.TaskDone {
							t.Errorf("review actual state = %s, want Done", actual.State)
						}
						if pending {
							found := false
							for _, m := range messages {
								if strings.Contains(m.Content, "exact late user instruction") {
									found = true
								}
							}
							if !found {
								t.Error("pending actual comment missing from reviewer")
							}
						}
						return "PASS: actual component paired checks", nil
					}
					if pending && !added {
						actual, err := plan.GetTask(d.Store, ids[0])
						if err != nil {
							return "", err
						}
						_, err = plan.AppendTaskComment(plan.WithProject(t.Context(), root), d.Store, ids[0], actual.Attempts, "user", "component-owner", "exact late user instruction")
						if err != nil {
							return "", err
						}
						added = true
					}
					return "component artifact", nil
				})
				var aid string
				d, aid, ids, root = roleHandoffFixture(t, 1, model)
				d.Sub.Mode = mode
				result, err := Launch(t.Context(), d, aid)
				if len(result) != 1 {
					t.Fatalf("expected one task, got %d: %v", len(result), err)
				}
				if err != nil || !result[0].Qualified() || reviews != 1 {
					t.Fatalf("review admission reviews=%d err=%v verification=%s reason=%s", reviews, err, result[0].Verification, result[0].VerificationReason)
				}
			})
		}
	}
}
