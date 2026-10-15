package agent

import (
	"context"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"

	"strings"
	"testing"
)

func TestContextProtectionRegressions(t *testing.T) {
	t.Run("intent after behavior", func(t *testing.T) {
		r := pruneRunner()
		msgs := []llm.Message{{Role: "system", Content: "base"}, {Role: "system", Content: "behavior"}, {Role: "user", Content: "tool result:\n" + strings.Repeat("intent", 1000)}, {Role: "assistant", Content: "reply"}, {Role: "user", Content: "tail"}}
		want := msgs[2].Content
		testPrune(r, msgs, 1000, true)
		if msgs[2].Content != want {
			t.Fatal("real intent was pruned")
		}
	})
	t.Run("unread output before note", func(t *testing.T) {
		r := pruneRunner()
		msgs := []llm.Message{{Role: "system", Content: "base"}, {Role: "user", Content: "intent"}, {Role: "user", Content: "tool result:\n" + strings.Repeat("fresh", 1000)}, {Role: "user", Content: "By the way: keep this"}}
		want := msgs[2].Content
		c := session.Continuation{Version: 1, BasisTokens: 1000, BasisChars: contentSize(msgs)}
		for i, m := range msgs {
			kind := []string{"injected", "intent", "output", "note"}[i]
			c.Add(kind, m.Role, m.Content, i == 2)
		}
		r.prune(&c)
		for i := range msgs {
			msgs[i] = c.Entries[i].Message
		}
		if msgs[2].Content != want {
			t.Fatal("unread tool observation was pruned before first solver request")
		}
	})
	t.Run("all schemas under retired percent cap", func(t *testing.T) {
		r := pruneRunner()
		msgs := []llm.Message{{Role: "system", Content: "base"}, {Role: "user", Content: "intent"}}
		for i := 0; i < 5; i++ {
			msgs = append(msgs, llm.Message{Role: "user", Content: "Schema for t:\n" + strings.Repeat("schema", 1000)})
		}
		msgs = append(msgs, llm.Message{Role: "user", Content: "tail"})
		testPrune(r, msgs, 1000, true)
		n := 0
		for _, m := range msgs {
			if strings.HasPrefix(m.Content, "Schema for ") {
				n++
			}
		}
		if n != 5 {
			t.Fatalf("retained%d schemas want5", n)
		}
	})
}
func TestContextVisibleSummaryIndependentOfReasoningUsage(t *testing.T) {
	model := &scriptModel{replies: []string{"VISIBLE_KEEP"}, usage: llm.Usage{In: 50, Out: 500, Reported: true}}
	r := compactRunner(model, &recordSink{}, testTemplateDir(t))
	msgs := []llm.Message{{Role: "system", Content: "base"}, {Role: "user", Content: strings.Repeat("old", 3000)}, {Role: "assistant", Content: "recent"}}
	_, sum, ran, err := testCompact(r, context.Background(), msgs, 950, 0)
	if err != nil || !ran || sum != "VISIBLE_KEEP" {
		t.Fatalf("visible summary should fit independently of reasoning tokens got%q ran%v err%v", sum, ran, err)
	}
}
