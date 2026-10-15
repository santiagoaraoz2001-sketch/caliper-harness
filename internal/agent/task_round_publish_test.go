package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

func TestTaskRoundCleanupDurableBeforePublish(t *testing.T) {
	a := &session.Assignment{Store: "/store", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "A", Attempt: 1}
	c := session.Continuation{Version: 1, Assignment: a}
	c.Add("intent", "user", "original constraint", false)
	c.AddGuidance("consumed coaching", session.RoundFor(a, ""))
	c.Add("note", "user", "do not remove this user constraint", false)
	c.Add("failure", "user", "unresolved", false)
	c.Add("evidence", "user", "actual current check and independent reviewer", false)
	for _, mode := range []string{"healthy", "sink", "missing", "cancel", "invalid correlation", "otherRound"} {
		t.Run(mode, func(t *testing.T) {
			r := &Runner{Continuation: c.Clone(), SessionID: "owned"}
			sink := &recordSink{}
			r.Session = sink
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			round := session.RoundFor(a, "")
			switch mode {
			case "sink":
				sink.err = errors.New("original sink cause")
			case "missing":
				r.Session = nil
			case "cancel":
				cancel()
			case "invalid correlation":
				r.Continuation.Add("output", "tool", "orphan result", false)
			case "otherRound":
				round.Attempt++
			}
			before := r.Continuation.Clone()
			err := r.retireTaskGuidance(ctx, round)
			if mode == "healthy" {
				if err != nil || len(sink.texts) != 1 || sink.kinds[0] != "continuation" || r.Continuation.Entries[1].Kind != "history" {
					t.Fatalf("publish: %v %+v", err, sink)
				}
				saved, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: sink.texts[0]}}, 32768)
				if err != nil || !reflect.DeepEqual(saved.Entries, r.Continuation.Entries) {
					t.Fatal("durable candidate differs", err)
				}
				for i := 2; i < len(c.Entries); i++ {
					if !reflect.DeepEqual(c.Entries[i], r.Continuation.Entries[i]) {
						t.Fatal("essential fact mutated")
					}
				}
			} else {
				if mode != "otherRound" && err == nil {
					t.Fatal("failure silently accepted")
				}
				if mode == "sink" && !errors.Is(err, sink.err) {
					t.Fatal("sink cause lost", err)
				}
				if !reflect.DeepEqual(before, r.Continuation) || len(sink.texts) != 0 {
					t.Fatal("failed/no-op cleanup changed original or published")
				}
			}
		})
	}
}

// Claim admission remains current, while preparing/claiming no longer replaces
// historical messages, native results, user facts or original reasoning.
func TestClaimPreparationRetainsContextAndReadmits(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "previous user intent", false)
	c.Add("task", "user", "actual current request", false)
	c.Add("reply", "assistant", "unsupported completion stays unverified", false)
	c.Add("note", "user", "exact user comment κ", false)
	c.Add("evidence", "user", "protected check/review evidence", false)
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: "native planning narrative", Reasoning: "original reasoning", Thinking: "original thinking", ToolCalls: []llm.ToolCall{{ID: "read-1", Name: "read", Arguments: `{"path":"facts.txt"}`}}}}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "read-1", Name: "read", Content: "exact source/hash/range"}})
	end := len(c.Entries)
	for _, mode := range []string{"healthy", "sink", "cancel", "missing", "restored", "admission", "invalid-group"} {
		t.Run(mode, func(t *testing.T) {
			sink := &recordSink{}
			admitted := 0
			r := &Runner{Continuation: c.Clone(), claimPreparationEnd: end, Session: sink, SessionID: "owned", Assigned: &AssignedOps{Admit: func(ctx context.Context) error {
				admitted++
				if mode == "admission" {
					return errors.New("actual task readmission failed")
				}
				return ctx.Err()
			}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "sink":
				sink.err = errors.New("unneeded replacement sink fails")
			case "cancel":
				cancel()
			case "missing":
				r.Session = nil
			case "restored":
				r.claimPreparationEnd = 0
			case "invalid-group":
				r.Continuation.Entries = r.Continuation.Entries[:end-1]
			}
			before := r.Continuation.Clone()
			err := r.archiveClaimPreparation(ctx)
			if !reflect.DeepEqual(before, r.Continuation) || len(sink.texts) != 0 {
				t.Fatal("claim boundary replaced history or published an archive")
			}
			if mode == "cancel" || mode == "admission" || mode == "invalid-group" {
				if err == nil || r.claimPreparationEnd != end {
					t.Fatal("failed admission cleared the pending boundary", err)
				}
				return
			}
			if err != nil || r.claimPreparationEnd != 0 {
				t.Fatal("readmission/boundary clearing failed", err)
			}
			want := 1
			if mode == "restored" {
				want = 0
			}
			if admitted != want {
				t.Fatalf("readmissions=%d want%d", admitted, want)
			}
		})
	}
}

func TestClaimNativeBookkeepingRetained(t *testing.T) {
	c := session.Continuation{Version: 1, Assignment: &session.Assignment{Store: "/store", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}}
	c.Add("intent", "user", "original request", false)
	for _, observed := range []struct{ id, name, kind, result string }{
		{"failed", "prepare_task", "failure", "entire call array refused; no calls ran: missing check_command"},
		{"prepared", "prepare_task", "output", "actual createPlan/createApproach/createTask/setTaskBlock effects"},
		{"ready", "ready_task", "output", "actual approach-ready receipt"},
		{"source", "read", "output", "actual source/hash/range"},
	} {
		c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Thinking: "provider thought κ", Reasoning: "provider reasoning", ReasoningContent: "opaque auxiliary", ToolCalls: []llm.ToolCall{{ID: observed.id, Name: observed.name, Arguments: `{}`}}}}, session.Entry{Kind: observed.kind, Message: llm.Message{Role: "tool", ToolCallID: observed.id, Name: observed.name, Content: observed.result}})
	}
	before := c.Clone()
	sink := &recordSink{}
	r := &Runner{Continuation: c, claimPreparationEnd: len(c.Entries), Session: sink, SessionID: "owned", Assigned: &AssignedOps{Admit: func(ctx context.Context) error { return ctx.Err() }}}
	if err := r.archiveClaimPreparation(context.Background()); err != nil || r.claimPreparationEnd != 0 || len(sink.kinds) != 0 || !reflect.DeepEqual(before, r.Continuation) {
		t.Fatal("preparation reasoning/call/results retired", err)
	}
	encoded, err := r.Continuation.Snapshot().Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: encoded}}, 32768)
	if err != nil || !reflect.DeepEqual(before.Entries, restored.Entries) {
		t.Fatal("retained groups lost on durable snapshot replay", err)
	}
}

func TestCapturedClaimNativeBookkeepingRetained(t *testing.T) {
	path := os.Getenv("SPECIFIC_CLAIM_ARCHIVE_CAPTURE")
	if path == "" {
		t.Skip("exact closed native capture supplied by evidence gate")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Continuation session.Continuation
		End          int
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	before := capture.Continuation.Clone()
	sink := &recordSink{}
	r := &Runner{Continuation: before.Clone(), claimPreparationEnd: capture.End, Session: sink, SessionID: "closed-capture", Assigned: &AssignedOps{Admit: func(ctx context.Context) error { return ctx.Err() }}}
	if err := r.archiveClaimPreparation(context.Background()); err != nil || len(sink.kinds) != 0 || !reflect.DeepEqual(before, r.Continuation) {
		t.Fatal("captured native preparation history changed", err)
	}
}
