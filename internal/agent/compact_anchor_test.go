package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

func TestCompactionRepeatedAnchorsRemainLossless(t *testing.T) {
	c := passedCompactionFixture()
	const refusal = "No calls ran. Remove unsupported verification. Current arguments: title, outcome, rules, articles, proof. Required outcome and proof are missing."
	for i := range 32 {
		c.Entries = append(c.Entries, session.Entry{
			Kind: "failure", Unread: i == 31,
			Message: llm.Message{Role: "tool", Name: "plan", ToolCallID: fmt.Sprintf("refusal-%d", i), Content: refusal},
		})
	}
	// Identical bytes can have different provenance; sharing text must not
	// merge entries or turn a user's note into a tool receipt.
	c.Add("note", "user", refusal, false)
	c.Add("failure", "user", refusal+"\nUnique: actual check exited 7; review is missing.", false)
	c.Add("note", "user", "Preserve exact Unicode κ and CRLF\r\n", false)
	c.Add("note", "user", "Preserve exact Unicode κ and CRLF\n", false)
	c.Add("evidence", "user", "", false)
	c.Add("evidence", "user", "", true)
	original := c.Clone()
	var expected []session.Entry
	for _, e := range c.Entries {
		switch e.Kind {
		case "intent", "task", "note", "failure", "evidence", "guidance":
			expected = append(expected, e)
		}
	}
	r := truthRunner(t, &scriptModel{}, 131072)
	judged := false
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judged = true
		var packet struct {
			Summary               string
			Anchors               []session.Entry
			RepeatedAnchorContent []struct{ Anchor, ContentFromAnchor int }
			Coding                *session.CodingState
			Assignment            *session.Assignment
			Workflow              *session.WorkflowScope
		}
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if len(packet.RepeatedAnchorContent) != 32 || len(packet.Anchors) != len(expected) {
			t.Fatalf("repeated facts not losslessly shared: refs=%d anchors=%d", len(packet.RepeatedAnchorContent), len(packet.Anchors))
		}
		for _, ref := range packet.RepeatedAnchorContent {
			if ref.Anchor < 0 || ref.Anchor >= len(packet.Anchors) || ref.ContentFromAnchor < 0 || ref.ContentFromAnchor >= ref.Anchor {
				t.Fatalf("invalid content reference: %+v", ref)
			}
			source := packet.Anchors[ref.ContentFromAnchor].Message.Content
			if source == "" || packet.Anchors[ref.Anchor].Message.Content != "" {
				t.Fatalf("reference does not name retained first body: %+v", ref)
			}
			for i := 0; i < ref.ContentFromAnchor; i++ {
				if packet.Anchors[i].Message.Content == source {
					t.Fatalf("reference did not use first body: %+v", ref)
				}
			}
			packet.Anchors[ref.Anchor].Message.Content = source
		}
		if !reflect.DeepEqual(packet.Anchors, expected) {
			t.Fatal("anchor content/order/provenance/identity/unread status changed")
		}
		if packet.Summary != "Unchanged inaccurate summary." || !reflect.DeepEqual(packet.Coding, c.Coding) || !reflect.DeepEqual(packet.Assignment, c.Assignment) || !reflect.DeepEqual(packet.Workflow, c.Workflow) {
			t.Fatal("summary or owning state changed")
		}
		if strings.Count(q.Context, refusal) != 2 { // first body plus the distinct extended failure
			t.Fatal("exact duplicate bodies remain, or a unique failure was lost")
		}
		return decision.Verdict{Option: q.Options[1], Confidence: .9}, nil
	}
	accepted, err := r.reviewCompaction(context.Background(), c, "unchanged raw source", "Unchanged inaccurate summary.")
	if err != nil || accepted || !judged || !reflect.DeepEqual(c, original) {
		t.Fatalf("rejected summary or projection changed original: accepted=%v judged=%v err=%v", accepted, judged, err)
	}
}
