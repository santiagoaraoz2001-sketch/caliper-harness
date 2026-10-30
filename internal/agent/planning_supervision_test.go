package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestSettledClaimDoesNotRejudgeEverySuccessfulDraft(t *testing.T) {
	question := "current task: answer proof"
	r := &Runner{Prompts: prompts.Builtin(), assignedClaim: true, settlement: &TaskSettlement{TaskID: "old", Attempt: 1, Qualified: true}, Session: &recordSink{}, SessionID: "run", LoopStall: 5,
		Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "store", Kind: "task", ID: "new"}},
		Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
			return BookkeepingFacts{Preparation: question, Checkpoint: ProgressCheckpoint{Phase: "PREPARE", PATStep: "proof", SelectedID: "new"}}, nil
		}}}
	r.enforcement.latest = &observed{Tool: "graphql", PlanningQuestion: question, Result: tool.Result{Text: "saved"}}
	judgments := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		return decision.Verdict{Option: q.Options[0], Confidence: 1}, nil
	}
	if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil || judgments != 0 {
		t.Fatal("historical claim caused redundant PAT judgment", err, judgments)
	}
	w := &watcher{discoveryCalls: 1, phaseFacts: [32]byte{1}}
	if _, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil }); err != nil || judgments != 0 {
		t.Fatal("ordinary draft phase caused immediate review", err, judgments)
	}
	w.discoveryCalls = 5
	if _, err := r.reviewDiscovery(t.Context(), w, func(string, string) error { return nil }); err != nil || judgments != 1 {
		t.Fatal("cadence supervision suppressed", err, judgments)
	}
	r.enforcement.latest.Result.Failed = true
	r.enforcement.latest.Error = "real new failure"
	if _, err := r.enforce(t.Context(), false, "", func(string, string) error { return nil }); err != nil || judgments != 2 {
		t.Fatal("failure supervision suppressed", err, judgments)
	}
}

func TestReviewerCapacityPreservesUnknownAttachmentsAndHumanIntent(t *testing.T) {
	for _, owned := range []bool{false, true} {
		r := &Runner{Prompts: prompts.Builtin(), Reviewing: owned, TaskSystem: "Frozen task scope: verify public consumer and preserve current failure", Continuation: session.Continuation{Coding: &session.CodingState{TaskID: "task", Attempt: 1}}}
		original := strings.Repeat("host review attachment; ", 4000)
		if owned {
			r.reviewRequest = original
		}
		r.Continuation.Add("intent", "user", original, false)
		p, err := r.progressEvidence(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		p.LatestSettled = &loopObservation{CallID: "failed", Tool: "check", Failed: true, Error: "current failed evidence"}
		before := r.Continuation.Entries[0].Message.Content
		for p.reduceForCapacity() {
		}
		if p.OriginalUserIntent != original {
			t.Fatal("unknown reviewer obligations or human intent were omitted")
		}
		if p.AuthoredTaskInstructions != r.TaskSystem || p.LatestSettled.Error != "current failed evidence" || r.Continuation.Entries[0].Message.Content != before {
			t.Fatal("scope, failure or original archive changed")
		}
	}
}

func TestReviewerCapturedCapacityPacket(t *testing.T) {
	// Optional historical replay supplements the mandatory synthetic controls;
	// absence of a local archive is not a skipped product gate.
	path := os.Getenv("CALIPER_REVIEW_CAPACITY_PACKET")
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p progressPacket
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Checkpoint.Phase != "REVIEW" || p.AuthoredTaskInstructions == "" || p.Coding == nil || p.Coding.TaskID == "" {
		t.Fatal("not the recorded owning reviewer packet")
	}
	p.hostReviewRequest = true // source fixture has exact launch-owned provenance
	scope := p.AuthoredTaskInstructions
	for p.reduceForCapacity() {
	}
	q := decision.Request{Context: "", Instructions: prompts.Builtin().Text("decision.discovery.instructions"), Options: []string{prompts.Builtin().Text("decision.discovery.continue"), prompts.Builtin().Text("decision.discovery.act"), prompts.Builtin().Text("decision.discovery.reconsider")}}
	body, _ := json.Marshal(p)
	q.Context = string(body)
	complete, _ := json.Marshal(q)
	if p.AuthoredTaskInstructions != scope || p.OriginalUserIntent == "" {
		t.Fatalf("unknown archived obligations were discarded: %d", len(complete))
	}
	var removed progressOmission
	for _, omission := range p.CapacityOmissions {
		if strings.HasPrefix(omission.Field, "OriginalUserIntent (host-assembled") {
			removed = omission
		}
	}
	if removed.SHA256 != "" {
		t.Fatal("unknown archived attachment was omitted without owned components")
	}
	t.Logf("historical packet body %d bytes; reduced complete request %d bytes; unknown archived attachment retained; no current owned decomposition available", len(raw), len(complete))
}

func TestReviewerRequestProvenanceIsFreshHostOnly(t *testing.T) {
	for _, mode := range []string{"fresh-review", "restored-intent", "legacy-human", "unowned"} {
		r := planningTransportRunner()
		r.Continuation = session.Continuation{Coding: &session.CodingState{TaskID: "task", Attempt: 1}}
		r.Reviewing = true
		r.TaskSystem = "frozen review scope"
		switch mode {
		case "restored-intent":
			r.Continuation.Add("intent", "user", "actual human instruction", false)
		case "legacy-human":
			r.History = []llm.Message{{Role: "user", Content: "actual human history"}}
		case "unowned":
			r.Continuation.Coding = nil
		}
		r.begin("host review with large attachments")
		if (r.reviewRequest != "") != (mode == "fresh-review") {
			t.Fatal("wrong original intent authority", mode)
		}
	}
}
