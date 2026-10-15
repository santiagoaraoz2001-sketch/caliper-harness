package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
)

// Owning launch supplies TaskSystem after a real claim. This component replay
// checks its Runner consumer with scripted model/judge and actual source reads;
// it does not manufacture a main-run claim or qualify worker completion.
func TestWorkerTaskSystemReachesProgressWithoutReplacingUserIntent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pick       int
		confidence float64
	}{
		{"continue", 0, .99}, {"uncertain", 1, .499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &discoveryModel{}
			for _, path := range []string{"0", "1", "2", "3", "4"} {
				model.replies = append(model.replies, discoveryReply(path))
			}
			instructions := "Frozen authored task: repair keyboard-start behavior while preserving menu shortcuts. Acceptance: Space starts the game exactly once. Declared check: node keyboard-start.test.mjs."
			instructions += "\n" + prompts.Builtin().Text("system.task.handoff")
			startup := prompts.Builtin().Text("system.task.worker_request")
			judgments := 0
			r := discoveryRunner(t, model, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				var p struct {
					AuthoredTaskInstructions, OriginalUserIntent, CurrentUserRequest, LatestSourceText string
					DeliveredUserComments                                                              []deliveredUserComment
					PlanningActive, CurrentClaim, Assigned                                             bool
					RecentReceipts                                                                     []loopObservation
				}
				if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
					t.Fatal(err)
				}
				if p.AuthoredTaskInstructions != instructions || p.OriginalUserIntent != "Human original request: preserve the existing interface" || p.CurrentUserRequest != startup {
					t.Fatalf("worker requirements lost or substituted for user intent: instructions=%q original=%q current=%q", p.AuthoredTaskInstructions, p.OriginalUserIntent, p.CurrentUserRequest)
				}
				if len(p.DeliveredUserComments) != 1 || p.DeliveredUserComments[0].Text != "USER COMMENT: keep the existing menu shortcuts" || p.PlanningActive || p.CurrentClaim || p.Assigned {
					t.Fatalf("authored instructions changed steering or invented main ownership: %+v", p)
				}
				if len(p.RecentReceipts) != 5 || p.RecentReceipts[4].Tool != "read" || !strings.Contains(p.LatestSourceText, "actual source 4") || strings.Contains(p.LatestSourceText, "keyboard-start") {
					t.Fatal("authored requirements replaced actual receipt evidence")
				}
				return decision.Verdict{Option: q.Options[tc.pick], Confidence: tc.confidence}, nil
			}, &recordSink{})
			r.TaskSystem = instructions
			r.Continuation.Version = 1
			r.Continuation.Add("intent", "user", "Human original request: preserve the existing interface", false)
			r.Continuation.Add("note", "user", "USER COMMENT: keep the existing menu shortcuts", false)
			if _, _, err := r.Run(t.Context(), startup); err != nil || judgments != 1 {
				t.Fatalf("worker progress route: judgments=%d err=%v", judgments, err)
			}
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "guidance" {
					t.Fatal("silent verdict inserted a model instruction")
				}
			}
			for _, request := range model.seen {
				count := 0
				for _, message := range request {
					if message.Content == instructions && message.Role == "system" {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("D1 evidence changed protected worker system role: copies=%d", count)
				}
			}
		})
	}
}

func TestWorkerTaskInstructionsSurviveProgressCapacityAdmission(t *testing.T) {
	for _, permanentlyRefused := range []bool{false, true} {
		r, w := largeProgressPacket(t)
		r.TaskSystem = "Frozen authored requirements: " + strings.Repeat("preserve required keyboard behavior; ", 256)
		attempts := 0
		ownerBefore, _ := json.Marshal([]any{r.Continuation, r.enforcement.current, w.recentReplies, w.recentReceipts, w.recentDeliberation, r.TaskSystem})
		sink := &recordSink{}
		r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
			attempts++
			var p struct {
				AuthoredTaskInstructions string
				CapacityOmissions        []progressOmission
			}
			if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
				t.Fatal(err)
			}
			if p.AuthoredTaskInstructions != r.TaskSystem {
				t.Fatal("native capacity fitting silently removed essential task requirements")
			}
			if attempts == 1 || permanentlyRefused {
				return decision.Verdict{}, &decision.ContextCapacityError{Message: "controlled native context refusal"}
			}
			if len(p.CapacityOmissions) == 0 {
				t.Fatal("optional dense evidence omitted without disclosure")
			}
			return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
		}
		advice, err := r.confirmLoop(t.Context(), w, "existing advisory cadence", func(kind, text string) error {
			return sink.Append(t.Context(), "essential-worker-instructions", kind, text)
		}, "")
		if permanentlyRefused {
			failures := 0
			for i, kind := range sink.kinds {
				if kind == "judgment-error" && strings.Contains(sink.texts[i], "controlled native context refusal") {
					failures++
				}
				if kind == "guidance" || kind == decision.KindJudgment {
					t.Fatal("exhausted admission fabricated a nudge or verdict")
				}
			}
			if err != nil || advice != "" || attempts < 2 || failures != 1 {
				t.Fatalf("failed advisory stopped work or lost evidence: advice=%q attempts=%d failures=%d err=%v", advice, attempts, failures, err)
			}
		} else if err != nil || advice != "" || attempts != 2 {
			t.Fatalf("existing capacity/silence behavior changed: advice=%q attempts=%d err=%v", advice, attempts, err)
		}
		ownerAfter, _ := json.Marshal([]any{r.Continuation, r.enforcement.current, w.recentReplies, w.recentReceipts, w.recentDeliberation, r.TaskSystem})
		if string(ownerBefore) != string(ownerAfter) {
			t.Fatal("advisory admission changed owning worker instructions or continuation")
		}
	}
}
