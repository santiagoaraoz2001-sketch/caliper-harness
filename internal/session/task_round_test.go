package session

import (
	"encoding/json"
	"reflect"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
)

func TestTaskRoundProtectionCloneAndRestore(t *testing.T) {
	a := &Assignment{Store: "/store", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "A", Attempt: 1}
	c := Continuation{Version: 1, Assignment: a}
	c.Add("intent", "user", "global constraint", false)
	c.AddGuidance("same advice", RoundFor(a, ""))
	c.AddGuidance("same advice", RoundFor(a, ""))
	b := *a
	b.TaskID = "B"
	c.AddGuidance("same advice", RoundFor(&b, ""))
	c.Add("note", "user", "user instruction remains across tasks", false)
	c.Entries[len(c.Entries)-1].Round = RoundFor(a, "comment")
	c.Add("guidance", "user", "legacy advice", false)
	c.Add("failure", "user", "unresolved failure", false)
	c.Add("evidence", "user", "exact check/review receipt", false)
	c.Entries = append(c.Entries, Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call", Name: "read", Arguments: `{"path":"source"}`}}}}, Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "call", Content: "source bytes\r\nα"}})
	if len(c.Entries) != 9 {
		t.Fatalf("dedup/different round: %d", len(c.Entries))
	}
	original := c.Clone()
	candidate := c.Clone()
	candidate.Entries[1].Round.TaskID = "changed"
	if c.Entries[1].Round.TaskID != "A" {
		t.Fatal("clone shared round pointer")
	}
	candidate = c.Clone()
	if !candidate.RetireGuidance(RoundFor(a, "")) {
		t.Fatal("no retirement")
	}
	if !reflect.DeepEqual(c, original) {
		t.Fatal("pure candidate changed original")
	}
	keep := candidate.Protected(3)
	for i, e := range candidate.Entries {
		if i == 1 {
			if keep[i] {
				t.Fatal("qualified advice not eligible")
			}
		} else if !keep[i] {
			t.Fatalf("essential entry %d kind=%s was lost", i, e.Kind)
		}
	}
	raw, err := candidate.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := Restore([]Event{{Kind: "continuation", Text: raw}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Entries, candidate.Entries) {
		t.Fatal("restore changed exact entries/provenance/call groups")
	}
	comment := outcome.TaskComment{ID: "actual-comment", TaskID: a.TaskID, Attempt: a.Attempt, Text: "cross-task constraint remains", Source: "user"}
	body, _ := json.Marshal(comment)
	tail, _, err := Restore([]Event{{Kind: "continuation", Text: raw}, {Kind: "task_comment", Text: string(body)}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	entry := tail.Entries[len(tail.Entries)-1]
	if entry.Round == nil || entry.Round.CommentID != comment.ID || entry.Round.Store != a.Store || entry.Message.Content != outcome.TaskCommentPrefix+string(body) || !tail.Protected(3)[len(tail.Entries)-1] {
		t.Fatal("typed raw delivery lost provenance, bytes or protection")
	}
	candidate.Entries[1].Round.Attempt = 0
	if candidate.Validate() == nil {
		t.Fatal("invalid provenance accepted")
	}
	var legacy Continuation
	encoded, _ := json.Marshal(original)
	_ = json.Unmarshal(encoded, &legacy)
	for i := range legacy.Entries {
		legacy.Entries[i].Round = nil
	}
	if legacy.RetireGuidance(RoundFor(a, "")) {
		t.Fatal("legacy provenance guessed")
	}
}

// Repeated final-repair advice must address the next model turn, without
// rewriting earlier native receipts, comments or the durable raw event log.
func TestRepeatedGuidanceMovesToSettledTail(t *testing.T) {
	const advice = "PREPARE: completion requested without a live task claim. Author or explicitly select the actual same-project PAT, obtain owning readiness and task_start; then execute it. Source diagnosis not yet observed must remain unknown."
	a := &Assignment{Store: "/store", Workspace: "/workspace", PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "unclaimed", true: "task round"}[bound], func(t *testing.T) {
			var round *TaskRound
			if bound {
				round = RoundFor(a, "")
			}
			c := Continuation{Version: 1, Assignment: a}
			c.Add("intent", "user", "original requirement", false)
			c.AddGuidance(advice, round)
			guidance := c.Entries[1]
			c.Add("note", "user", "exact user comment κ\r\n", false)
			c.Entries[2].Round = RoundFor(a, "actual-comment")
			c.Entries = append(c.Entries, Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "read-1", Name: "read", Arguments: `{"path":"actual.go"}`}}}}, Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "read-1", Content: "exact source/hash\r\nκ"}})
			c.Add("reply", "assistant", "first provisional answer", false)
			c.Add("reply", "assistant", "second provisional answer", false)
			before := c.Clone()
			c.AddGuidance(advice, round)
			want := before.Clone()
			want.Entries = append(want.Entries[:1], want.Entries[2:]...)
			want.Entries = append(want.Entries, guidance)
			if !reflect.DeepEqual(c, want) || c.Messages()[len(c.Entries)-1].Role != "user" {
				t.Fatal("reissued repair is not the single exact tail entry, or retained facts changed")
			}
			if !c.Protected(3)[len(c.Entries)-1] || !c.Entries[3].Unread {
				t.Fatal("guidance/output protection changed")
			}
			c.AddGuidance(advice, round)
			if !reflect.DeepEqual(c, want) {
				t.Fatal("already-tail repair was duplicated or changed")
			}
			raw, err := c.Encode()
			if err != nil {
				t.Fatal(err)
			}
			restored, _, err := Restore([]Event{{Kind: "continuation", Text: raw}}, 32768)
			if err != nil || !reflect.DeepEqual(restored.Entries, c.Entries) {
				t.Fatal("tail placement or native/comment provenance lost on restore", err)
			}
		})
	}
}

func TestGuidanceDoesNotMoveNativeAssociations(t *testing.T) {
	for _, message := range []llm.Message{
		{Role: "assistant", Content: "advice", ToolCalls: []llm.ToolCall{{ID: "call", Name: "read", Arguments: `{"path":"source"}`}}},
		{Role: "user", Content: "advice", ToolCallID: "call"},
		{Role: "user", Content: "advice", Name: "read"},
	} {
		c := Continuation{Version: 1, Entries: []Entry{{Kind: "guidance", Message: message}, {Kind: "output", Unread: true, Message: llm.Message{Role: "tool", ToolCallID: "call", Name: "read", Content: "actual receipt"}}}}
		before := c.Clone()
		c.AddGuidance("advice", nil)
		if len(c.Entries) != len(before.Entries)+1 || !reflect.DeepEqual(c.Entries[:len(before.Entries)], before.Entries) {
			t.Fatal("malformed correlated guidance was moved or swallowed the new standalone repair")
		}
	}
}
