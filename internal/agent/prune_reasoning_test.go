package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

func reasoningAliasFixture(field string, age int) session.Continuation {
	old := llm.Message{Role: "assistant", Content: "unchanged answer", ToolCalls: []llm.ToolCall{{ID: "read-id", Name: "read", Arguments: `{"path":"main.go"}`}}}
	text := strings.Repeat("settled reasoning λ ", 400)
	switch field {
	case "reasoning":
		old.Reasoning = text
	case "reasoning_content":
		old.ReasoningContent = text
	case "thinking":
		old.Thinking = text
	case "all":
		old.Reasoning, old.ReasoningContent, old.Thinking = text, text, text
	}
	result := llm.Message{Role: "tool", Name: "read", ToolCallID: "read-id", Content: "settled"}
	return session.Continuation{Version: 1, BasisChars: 1, BasisTokens: 1, Entries: []session.Entry{
		{Kind: "intent", Message: llm.Message{Role: "user", Content: "Inspect main.go."}},
		{Kind: "reply", Message: old},
		{Kind: "output", Message: result},
		{Kind: "reply", Message: llm.Message{Role: "assistant", Content: strings.Repeat("x", age-result.Size())}},
	}}
}

func TestPruneReasoningAliasesPreservedAtEveryAge(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content", "thinking", "all"} {
		for _, age := range []int{49999, 50000, 50001} {
			t.Run(fmt.Sprintf("%s/%d", field, age), func(t *testing.T) {
				c := reasoningAliasFixture(field, age)
				original := c.Clone()
				_, report := agePolicyRunner(t).prune(&c)
				got, before := c.Entries[1].Message, original.Entries[1].Message
				for name, pair := range map[string][2]string{
					"reasoning":         {before.Reasoning, got.Reasoning},
					"reasoning_content": {before.ReasoningContent, got.ReasoningContent},
					"thinking":          {before.Thinking, got.Thinking},
				} {
					if pair[0] != pair[1] {
						t.Fatalf("%s was pruned at age %d: report=%q", name, age, report)
					}
				}
				if !reflect.DeepEqual(got, before) || !reflect.DeepEqual(c.Entries[0], original.Entries[0]) || !reflect.DeepEqual(c.Entries[2:], original.Entries[2:]) {
					t.Fatal("pruning changed visible text, calls, result correlation or recent history")
				}
				if err := c.Snapshot().Validate(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPruneReasoningAliasesRespectProtectedAndCurrentThought(t *testing.T) {
	for _, protection := range []string{"unread-reply", "unread-result", "failure", "evidence", "schema", "current"} {
		t.Run(protection, func(t *testing.T) {
			c := reasoningAliasFixture("all", 50001)
			switch protection {
			case "unread-reply":
				c.Entries[1].Unread = true
			case "unread-result":
				c.Entries[2].Unread = true
			case "current":
				c.Entries = c.Entries[:2]
				c.Entries[1].Message.ToolCalls = nil
			default:
				c.Entries[2].Kind = protection
			}
			before := c.Clone()
			if _, report := agePolicyRunner(t).prune(&c); report != "" || !reflect.DeepEqual(c, before) {
				t.Fatalf("protected/current thought changed: %q", report)
			}
		})
	}
}

func TestPruneReasoningAliasesPreservedAtExistingBatchBoundary(t *testing.T) {
	r := agePolicyRunner(t)
	c := reasoningAliasFixture("all", 49999)
	var batch pruneBatch
	batch.apply(r, &c)
	c.Add("reply", "assistant", "new context makes prior thought old enough", false)
	before := c.Clone()
	if _, report := batch.apply(r, &c); report != "" || !reflect.DeepEqual(c, before) {
		t.Fatal("reasoning aliases rewrote cached context between boundaries")
	}
	c.SchemaKey = "next existing tool phase"
	before = c.Clone()
	if _, report := batch.apply(r, &c); report != "" || !reflect.DeepEqual(c, before) {
		t.Fatalf("existing phase boundary changed reasoning: %q", report)
	}
}
