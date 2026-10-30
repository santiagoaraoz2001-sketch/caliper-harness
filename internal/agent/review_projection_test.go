package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

func TestReviewNormalAdmissionProjectsOwnedInputsWithoutChangingEvidence(t *testing.T) {
	coding := &session.CodingState{TaskID: "task", Attempt: 1}
	check, _ := json.Marshal(coding)
	inputs := &ReviewInputs{Instructions: "rendered evidence required; UNVERIFIED when absent", Verify: "inspect actual browser rendering", Tests: "empty and populated consumers", Current: "inherited baseline", Sketch: "preserve keyboard behavior", UnverifiedWorkerReport: "Browser NOT inspected; success only claimed", HostExecutionEvidence: "13 tests passed; semantic coverage unknown", FileObservations: json.RawMessage(`[{"CallID":"edit-1","Result":"partial failure; old_sha256=abc; preview omitted"}]`), InitialCheck: check}
	original := strings.Repeat("known launch attachment; ", 1000)
	r := planningTransportRunner()
	r.Prompts, r.Reviewing, r.TaskSystem, r.ReviewInputs = prompts.Builtin(), true, "frozen target and acceptance", inputs
	r.Continuation = session.Continuation{Coding: coding}
	r.begin(original)
	r.Continuation.Add("note", "user", "USER COMMENT: keep my existing data — café", false)
	r.Continuation.Add("task", "user", "Actual later human constraint", false)
	r.Continuation.Add("note", "user", r.Prompts.Text("continuation.task_comment_prefix")+`{"ID":"comment-1","Text":"task advice, not approval"}`, false)
	w := &watcher{recentReceipts: []*loopObservation{{CallID: "failed-now", Tool: "check", Error: "current check failed", Failed: true}}}
	w.history.facts = []discoveryFact{{source: &sourceCoverage{Path: "style.css", FileSHA256: "same-version", CompleteRead: true, Reads: 16}}, {query: &repeatedObservation{Tool: "grep", LastCallID: "repeated", Occurrences: 14}}}
	before, _ := json.Marshal([]any{r.Continuation, inputs, w.recentReceipts, w.history.evidence()})
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	judgments := 0
	r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
		judgments++
		var got progressPacket
		if err := json.Unmarshal([]byte(req.Context), &got); err != nil {
			t.Fatal(err)
		}
		if got.OriginalUserIntent != "" || got.Review == nil || got.HostReviewAttachment == nil || len(got.CapacityOmissions) != 0 {
			t.Fatal("not normal typed projection", req.Context)
		}
		if got.HostReviewAttachment.Bytes != len(original) || got.HostReviewAttachment.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(original))) {
			t.Fatal("attachment provenance lost")
		}
		if got.Review.Verify != inputs.Verify || got.Review.Tests != inputs.Tests || got.Review.Current != inputs.Current || got.Review.Sketch != inputs.Sketch || got.Review.UnverifiedWorkerReport != inputs.UnverifiedWorkerReport || got.Review.HostExecutionEvidence != inputs.HostExecutionEvidence || string(got.Review.FileObservations) != string(inputs.FileObservations) || got.Review.Instructions != inputs.Instructions {
			t.Fatal("unique obligations or evidence lost", got.Review)
		}
		if got.Review.InitialCheck != nil || got.Coding.TaskID != "task" || got.Coding.Attempt != 1 {
			t.Fatal("current check duplicated or lost")
		}
		if got.CurrentUserRequest != "Actual later human constraint" || len(got.DeliveredUserComments) != 1 || len(got.DeliveredTaskComments) != 1 || got.DeliveredTaskComments[0].ID != "comment-1" {
			t.Fatal("delivered human/task context lost")
		}
		if got.AuthoredTaskInstructions != r.TaskSystem || got.Checkpoint.Phase != "REVIEW" || got.LatestSettled.Error != "current check failed" || got.Discovery.Sources[0].Reads != 16 || got.Discovery.RepeatedObservations[0].Occurrences != 14 {
			t.Fatal("current role/failure/discovery evidence lost")
		}
		return decision.Verdict{Option: req.Options[0], Confidence: .9}, nil
	}
	if _, err := r.progressAdvice(t.Context(), w, p, "discovery-checkpoint", "controlled transport test", func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if judgments != 1 {
		t.Fatal("unexpected admission/retry", judgments)
	}
	// Advice updates watcher phase bookkeeping; owning continuation, inputs,
	// receipts and discovery facts remain intact.
	after, _ := json.Marshal([]any{r.Continuation, inputs, w.recentReceipts, w.history.evidence()})
	if inputs.InitialCheck == nil {
		t.Fatal("owner context was mutated")
	}
	if string(after) != string(before) {
		t.Fatal("review continuation/owned inputs/evidence changed")
	}
}

func TestReviewProjectionPreservesUnknownAndRestoredHumanContext(t *testing.T) {
	for _, mode := range []string{"no-inputs", "not-owned", "restored-human", "legacy-history"} {
		t.Run(mode, func(t *testing.T) {
			human := strings.Repeat("Review-looking actual human requirements; ", 300)
			r := planningTransportRunner()
			r.Prompts, r.Reviewing, r.TaskSystem, r.ReviewInputs = prompts.Builtin(), true, "frozen scope", &ReviewInputs{Verify: "unique obligation"}
			r.Continuation = session.Continuation{Coding: &session.CodingState{TaskID: "task", Attempt: 1}}
			switch mode {
			case "no-inputs":
				r.ReviewInputs = nil
			case "not-owned":
				r.Continuation.Coding = nil
			case "restored-human":
				r.Continuation.Add("intent", "user", human, false)
			case "legacy-history":
				r.History = []llm.Message{{Role: "user", Content: human}}
			}
			r.begin(human)
			p, err := r.progressEvidence(t.Context(), nil)
			if err != nil || p.OriginalUserIntent != human || p.Review != nil || p.HostReviewAttachment != nil {
				t.Fatal("unknown/human attachment projected", err)
			}
			for p.reduceForCapacity() {
			}
			if p.OriginalUserIntent != human {
				t.Fatal("capacity discarded unknown/human obligations")
			}
		})
	}
}

func TestReviewProjectionRetainsDifferentInitialCheck(t *testing.T) {
	p := progressPacket{hostReviewRequest: true, OriginalUserIntent: "owned", AuthoredTaskInstructions: "frozen", Coding: &session.CodingState{TaskID: "task", Attempt: 2}}
	initial := json.RawMessage(`{"task_id":"task","attempt":1}`)
	p.projectReview(&ReviewInputs{InitialCheck: initial})
	if p.Review == nil || string(p.Review.InitialCheck) != string(initial) || p.Coding.Attempt != 2 {
		t.Fatal("different historical check erased/replaced")
	}
}

func TestReviewProjectionReferencesOnlyIdenticalProtectedFields(t *testing.T) {
	for _, known := range []bool{false, true} {
		inputs := &ReviewInputs{Title: "Frozen title", TaskRequest: "frozen request", Target: "target", Acceptance: "acceptance", Verify: "unique browser obligation", Tests: "unique consumer assertions", Workspace: "workspace", CheckCommand: "suite", AcceptanceCommand: "behavior"}
		coding := &session.CodingState{TaskID: "task", Attempt: 1, Workspace: inputs.Workspace, Command: inputs.CheckCommand, AcceptanceCommand: inputs.AcceptanceCommand}
		inputs.InitialCheck, _ = json.Marshal(coding)
		role := "custom role that does not reproduce all frozen fields"
		if known {
			role = prompts.Builtin().Format("agent.reviewer_role", inputs.Title, inputs.TaskRequest, inputs.Target, inputs.Acceptance)
		}
		p := progressPacket{hostReviewRequest: true, OriginalUserIntent: "owned attachment", AuthoredTaskInstructions: role, Coding: coding}
		p.projectReview(inputs)
		if p.Review.Verify != inputs.Verify || p.Review.Tests != inputs.Tests {
			t.Fatal("unique obligation was treated as a duplicate")
		}
		if known && (p.Review.FrozenTaskSource == "" || p.Review.TaskRequest != "" || p.Review.Target != "" || p.Review.Acceptance != "") {
			t.Fatal("identical protected fields still duplicated")
		}
		if !known && (p.Review.TaskRequest != inputs.TaskRequest || p.Review.Target != inputs.Target || p.Review.Acceptance != inputs.Acceptance || p.Review.FrozenTaskSource != "") {
			t.Fatal("custom role lost unique fields")
		}
		if p.Review.CheckSource == "" || p.Review.Workspace != "" || p.Review.InitialCheck != nil {
			t.Fatal("identical current check still duplicated")
		}
	}
}
