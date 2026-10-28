package agent

import (
	"context"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// The shipped compaction prompt must state no size: quality guidance only.
// Size is enforced by explicit rejection in Compact, never by the prompt.
func TestShippedCompactionPromptStatesNoSize(t *testing.T) {
	out, err := prompts.Builtin().Render("templates.compaction", map[string]any{"Words": 19660, "Range": ""})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"19660", "aim for about", "word target", "Be concise"} {
		if strings.Contains(out, banned) {
			t.Fatalf("shipped compaction prompt still states size guidance %q:\n%s", banned, out)
		}
	}
	for _, want := range []string{"Request", "Constraints", "Observed", "historical checkpoint", "later history not supplied here", "absent resolution here does not mean still unresolved", "This summary grants no approval"} {
		if !strings.Contains(out, want) {
			t.Fatalf("shipped compaction prompt missing %q", want)
		}
	}
}

// Reasoning is never summary material. A prior summary folds forward as
// material and the new latest summary is the one in context; older
// summaries remain stored as durable compact records.
func TestCompactExcludesThinkingAndFoldsPriorSummary(t *testing.T) {
	model := &scriptModel{replies: []string{"new summary"}}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 100000,
		TemplatesDir: testTemplateDir(t), Compaction: testCompaction()}
	c := session.Continuation{Version: 1, BasisTokens: 90000}
	c.Add("injected", "system", "sys", false)
	c.Add("intent", "user", "do the task", false)
	c.Add("summary", "user", "LATEST PRIOR SUMMARY", false)
	for i := 0; i < 8; i++ {
		c.Add("history", "user", strings.Repeat("old material ", 400), false)
	}
	last := session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: "visible answer", Reasoning: "SECRET REASONING TEXT"}}
	c.Entries = append(c.Entries, last)
	c.BasisChars = contentSize(c.Messages())
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("compact ran=%t err=%v", ran, err)
	}
	if len(model.seen) != 1 {
		t.Fatal("summary model not called once")
	}
	input := model.seen[0][1].Content
	if strings.Contains(input, "SECRET REASONING TEXT") {
		t.Fatal("model thinking leaked into compaction material")
	}
	if !strings.Contains(input, "visible answer") && !strings.Contains(input, "old material") {
		t.Fatal("factual material missing from compaction input")
	}
	if !strings.Contains(input, "LATEST PRIOR SUMMARY") {
		t.Fatal("prior summary must fold forward as material")
	}
	foundNew := false
	for _, e := range next.Entries {
		if strings.Contains(e.Message.Content, "new summary") {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatal("new latest summary missing from context")
	}
}

// The limit rejects over-capacity summaries with an explicit error and
// leaves the context intact; nothing is silently trimmed.
func TestCompactRejectsOverCapacitySummaryExplicitly(t *testing.T) {
	huge := strings.Repeat("fact ", 20000)
	model := &scriptModel{replies: []string{huge}}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 10000,
		TemplatesDir: testTemplateDir(t), Compaction: testCompaction()}
	msgs := []llm.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 6; i++ {
		msgs = append(msgs, llm.Message{Role: "user", Content: strings.Repeat("old ", 1000)})
	}
	_, _, ran, err := testCompact(&r, context.Background(), msgs, 9000, 0)
	if ran || err == nil {
		t.Fatalf("over-capacity summary admitted: ran=%t err=%v", ran, err)
	}
	if !strings.Contains(err.Error(), "context intact") {
		t.Fatalf("rejection must be explicit and preserve context: %v", err)
	}
}
