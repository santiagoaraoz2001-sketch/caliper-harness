package agent

import (
	"context"
	"encoding/json"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestHistoricalCommentDoesNotRetriggerEverySuccessfulReceipt(t *testing.T) {
	r := truthRunner(t, &scriptModel{}, 32768)
	r.Assigned = &AssignedOps{Facts: func(context.Context) (any, error) { return map[string]string{"state": "running"}, nil }}
	r.Continuation.Workflow = &session.WorkflowScope{Kind: "task", ID: "task"}
	r.assignedClaim = true
	r.Continuation.Add("note", "user", "USER COMMENT: keep the visible controls", false)
	calls := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		var p progressPacket
		if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
			t.Fatal(err)
		}
		if len(p.DeliveredUserComments) == 0 {
			t.Fatal("historical user authority erased from judgment")
		}
		return decision.Verdict{Option: coherent, Confidence: .8}, nil
	}
	log := func(string, string) error { return nil }
	r.enforcement.latest = &observed{Tool: "read", Result: tool.Result{Text: "first actual read"}}
	if _, err := r.enforce(t.Context(), false, "", log); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		r.enforcement.latest = &observed{Tool: "read", Result: tool.Result{Text: "new actual receipt"}}
		if _, err := r.enforce(t.Context(), false, "", log); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("historical comment caused %d judgments, want one", calls)
	}
	// Compaction may move the note's index; it has not delivered a new note.
	r.Continuation.Entries = append([]session.Entry{{Kind: "summary"}}, r.Continuation.Entries...)
	_, _ = r.enforce(t.Context(), false, "", log)
	if calls != 1 {
		t.Fatal("entry relocation looked like new user input")
	}
	r.Continuation.Add("note", "user", "USER COMMENT: keep the visible controls", false)
	_, _ = r.enforce(t.Context(), false, "", log)
	if calls != 2 {
		t.Fatal("a newly repeated real comment was ignored")
	}
	r.enforcement.latest = &observed{Tool: "read", Error: "actual new failure"}
	_, _ = r.enforce(t.Context(), false, "", log)
	if calls != 3 {
		t.Fatal("current failure bypassed advice")
	}
	_, _ = r.enforce(t.Context(), true, "actual final claim", log)
	if calls != 4 {
		t.Fatal("final claim bypassed advice")
	}
}
