package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Controlled owning callback/sink exercise atomic continuation publication;
// the run package tests actual Kuzu transitions and next-request delivery.
func TestPreparationRecoveryPublication(t *testing.T) {
	for _, mode := range []string{"healthy", "combined knowledge", "knowledge only", "owning unavailable", "sink", "missing", "cancelled", "cancel after append", "invalid correlation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := &recordSink{}
			r := &Runner{Assigned: &AssignedOps{Store: "/store"}, Workspace: "/workspace", SessionID: "session", Session: sink, Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "/store", Kind: "root"}, Version: 1, Measured: true, Prompt: 123}}
			r.Continuation.Add("intent", "user", "preserve exact requested behavior", false)
			r.Continuation.Add("guidance", "user", "old owning preparation recipe", false)
			r.Continuation.Entries[1].PreparationScope = r.preparationScope()
			r.Continuation.AddGuidance("legacy guidance must survive", nil)
			r.Continuation.Add("note", "user", "user constraint must survive", false)
			r.Continuation.Entries = append(r.Continuation.Entries,
				session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual", Name: "graphql", Arguments: `{"query":"{version}"}`}}}},
				session.Entry{Kind: "failure", Unread: true, Message: llm.Message{Role: "tool", Name: "graphql", ToolCallID: "actual", Content: "actual failure with unknown effects; readback required"}})
			foreign := session.Entry{Kind: "guidance", PreparationScope: "other-session\n/workspace", Message: llm.Message{Role: "user", Content: "other scope"}}
			r.Continuation.Entries = append(r.Continuation.Entries, foreign)
			r.preparationDisposition = "no calls ran; no IDs were returned; actual deficient field"
			r.enforcement.prepare = true
			repair := "actual PAT-only decision request"
			r.Assigned.Facts = func(context.Context) (any, error) {
				if mode == "owning unavailable" {
					return nil, errors.New("owning unavailable cause")
				}
				return BookkeepingFacts{Preparation: "read back actual existing task before the next legal operation"}, nil
			}
			switch mode {
			case "combined knowledge":
				r.enforcement.knowledge = true
				repair = "actual both PAT and product-knowledge request"
			case "knowledge only":
				r.enforcement.prepare, r.enforcement.knowledge = false, true
				repair = "actual product-knowledge request"
			case "sink":
				sink.err = errors.New("original sink failure")
			case "missing":
				r.Session = nil
			case "cancelled":
				cancel()
			case "cancel after append":
				r.Session = &preparationCancellingSink{sink: sink, cancel: cancel}
			case "invalid correlation":
				r.Continuation.Add("output", "tool", "orphan result", false)
			}
			before := r.Continuation.Clone()
			other := ""
			if r.enforcement.knowledge {
				other = "actual product-knowledge request"
			}
			delivered, err := r.publishPreparationGuidance(ctx, repair, r.enforcement.prepare, other)
			if mode == "healthy" || mode == "combined knowledge" || mode == "knowledge only" {
				if err != nil || !delivered || len(sink.texts) != 1 || sink.kinds[0] != "continuation" || r.Continuation.Measured {
					t.Fatalf("publish delivered%t err%v events%+v", delivered, err, sink)
				}
				want := append([]session.Entry(nil), before.Entries[:1]...)
				want = append(want, before.Entries[2:]...)
				if !reflect.DeepEqual(want, r.Continuation.Entries[:len(want)]) {
					t.Fatal("source/native groups/user/legacy/foreign guidance mutated")
				}
				if mode != "healthy" && r.Continuation.Entries[len(want)].Message.Content != other {
					t.Fatal("unresolved product knowledge was suppressed")
				}
				last := r.Continuation.Entries[len(r.Continuation.Entries)-1]
				if !r.ownsPreparationGuidance(last) || !strings.Contains(last.Message.Content, "actual deficient field") {
					t.Fatal("actual settled refusal missing")
				}
				saved, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: sink.texts[0]}}, 32768)
				if err != nil || !reflect.DeepEqual(saved.Entries, r.Continuation.Entries) {
					t.Fatal("durable replacement differs", err)
				}
			} else {
				if err == nil || delivered || !reflect.DeepEqual(before, r.Continuation) {
					t.Fatal("failed publication changed active continuation", err)
				}
				if mode == "sink" && !errors.Is(err, sink.err) {
					t.Fatal("sink cause lost", err)
				}
				if mode != "cancel after append" && len(sink.texts) != 0 {
					t.Fatal("failed candidate was recorded")
				}
				if mode == "cancel after append" && len(sink.texts) != 1 {
					t.Fatal("actual durable append before cancellation lost")
				}
			}
		})
	}
}

type preparationCancellingSink struct {
	sink   *recordSink
	cancel context.CancelFunc
}

func (s *preparationCancellingSink) Append(ctx context.Context, id, kind, text string) error {
	err := s.sink.Append(ctx, id, kind, text)
	s.cancel()
	return err
}
