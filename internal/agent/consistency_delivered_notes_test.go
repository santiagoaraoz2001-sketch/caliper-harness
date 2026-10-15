package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

type consistencyNoteEvidence struct {
	OriginalUserIntent, CurrentUserRequest string
	DeliveredUserComments                  []struct {
		EntryIndex int
		Text       string
	}
	DeliveredTaskComments []outcome.TaskComment
}

func consistencyDeliveredNotes(t *testing.T, snapshot bool) session.Continuation {
	t.Helper()
	pc := prompts.Builtin()
	events := []session.Event{{Kind: "prompt", Text: "build the requested snake game"}, {Kind: "prompt", Text: "implement the next declared task"}, {Kind: "btw", Text: "Keep touch controls and preserve the dark palette α🙂"}}
	for _, source := range []string{"user", "model-authored", "api-authored"} {
		raw, err := json.Marshal(outcome.TaskComment{ID: "comment-" + source, TaskID: "actual-task", Attempt: 2, Source: source, SessionRef: "observed-source-session", Text: "advice from " + source, CreatedAt: "fixture timestamp"})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, session.Event{Kind: "task_comment", Text: string(raw)})
	}
	c, _, err := session.RestoreWithPrompts(events, 32768, pc)
	if err != nil {
		t.Fatal(err)
	}
	c.Add("guidance", "user", "USER COMMENT: this is harness guidance", false)
	c.Add("output", "user", "USER COMMENT: this is tool output", true)
	c.Add("reply", "assistant", "USER COMMENT: this is model prose", false)
	c.Add("note", "user", "unclassified note is not human intent", false)
	if snapshot {
		raw, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		c, _, err = session.RestoreWithPrompts([]session.Event{{Kind: "continuation", Text: raw}}, 32768, pc)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func checkConsistencyDeliveredNotes(t *testing.T, raw []byte) {
	t.Helper()
	var e consistencyNoteEvidence
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.OriginalUserIntent != "build the requested snake game" || e.CurrentUserRequest != "implement the next declared task" {
		t.Fatalf("human request provenance changed: %+v", e)
	}
	if len(e.DeliveredUserComments) != 1 || e.DeliveredUserComments[0].EntryIndex != 2 || e.DeliveredUserComments[0].Text != "USER COMMENT: Keep touch controls and preserve the dark palette α🙂" {
		t.Fatalf("delivered human steering missing/misclassified: %+v", e.DeliveredUserComments)
	}
	if len(e.DeliveredTaskComments) != 3 {
		t.Fatalf("typed task advice missing: %+v", e.DeliveredTaskComments)
	}
	for i, source := range []string{"user", "model-authored", "api-authored"} {
		c := e.DeliveredTaskComments[i]
		if c.Source != source || c.ID != "comment-"+source || c.TaskID != "actual-task" || c.Attempt != 2 || c.Text != "advice from "+source || c.CreatedAt != "fixture timestamp" {
			t.Fatalf("typed comment provenance changed: %+v", c)
		}
	}
}

// Raw session and bounded-snapshot restores use their actual note extraction.
// No inference occurs; decisions are explicit component doubles.
func TestConsistencyDeliveredNotesReachBothDecisionInputs(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "snapshot"}[snapshot], func(t *testing.T) {
			r := thinkingRunner(nil)
			r.Prompts = prompts.Builtin()
			r.Continuation = consistencyDeliveredNotes(t, snapshot)
			t.Run("progress", func(t *testing.T) {
				packet, err := r.progressEvidence(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(packet)
				if err != nil {
					t.Fatal(err)
				}
				checkConsistencyDeliveredNotes(t, raw)
				packet.LatestSourceText = strings.Repeat("dense optional source body ", 8000)
				for packet.reduceForCapacity() {
				}
				raw, err = json.Marshal(packet)
				if err != nil {
					t.Fatal(err)
				}
				checkConsistencyDeliveredNotes(t, raw)
			})
			t.Run("thinking", func(t *testing.T) {
				r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
					metadata, _, ok := strings.Cut(req.Context, "\nUnit:\n")
					if !ok {
						t.Fatal("thinking evidence missing unit framing")
					}
					checkConsistencyDeliveredNotes(t, []byte(metadata))
					return decision.Verdict{Pick: 1, Option: loopProgress, Confidence: 1}, nil
				}
				if _, err := thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, false, nil); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestConsistencyThinkingPhaseUsesSelectedPlanningAndClaim(t *testing.T) {
	for _, tc := range []struct {
		name, phase       string
		selected, claimed bool
	}{
		{"ordinary registered AssignedOps", "ordinary generation", false, false},
		{"selected planning", "unclaimed main preparation", true, false},
		{"claimed matching execution", "claimed direct main execution", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := thinkingRunner(func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				meta, _, _ := strings.Cut(q.Context, "\nUnit:\n")
				var e struct {
					Phase   string
					Claimed bool
				}
				if err := json.Unmarshal([]byte(meta), &e); err != nil {
					t.Fatal(err)
				}
				if e.Phase != tc.phase || e.Claimed != tc.claimed {
					t.Fatalf("phase=%+v want=%s claimed=%t", e, tc.phase, tc.claimed)
				}
				return decision.Verdict{Pick: 1, Option: loopProgress, Confidence: 1}, nil
			})
			r.Assigned = &AssignedOps{}
			if tc.selected {
				r.Continuation.Workflow = &session.WorkflowScope{Store: "component", Kind: "task", ID: "fixture-task"}
			}
			if tc.claimed {
				// Explicit component owner state; no native execution is claimed.
				r.Workspace = "/component"
				r.assignedClaim = true
				r.Outcome.Status = outcome.Unknown
				r.Continuation.Assignment = &session.Assignment{Store: "component", Workspace: r.Workspace, TaskID: "fixture-task", Attempt: 1}
				r.Continuation.Coding = &session.CodingState{Workspace: r.Workspace, TaskID: "fixture-task", Attempt: 1}
			}
			if _, err := thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, false, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
