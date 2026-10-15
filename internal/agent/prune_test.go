package agent

import (
	"context"
	"strings"

	"gopkg.in/yaml.v3"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Explicit fixture provenance: this test adapter is not product classification.
func testPrune(r *Runner, msgs []llm.Message, total int, have bool) (int, string) {
	c := session.Continuation{Version: 1}
	prefix := 0
	for prefix < len(msgs) && msgs[prefix].Role == "system" {
		prefix++
	}
	lastReply := -1
	for i, m := range msgs {
		if m.Role == "assistant" {
			lastReply = i
		}
	}
	for i, m := range msgs {
		kind := "history"
		unread := false
		if i < prefix {
			kind = "injected"
		} else if i == prefix {
			kind = "intent"
		} else if m.Role == "user" && strings.HasPrefix(m.Content, "Schema for ") {
			kind = "schema"
		} else if m.Role == "user" && strings.HasPrefix(m.Content, "tool result:") {
			kind = "output"
			unread = i > lastReply && i == len(msgs)-1
		}
		c.Entries = append(c.Entries, session.Entry{Kind: kind, Message: m.Clone(), Unread: unread})
	}
	if have {
		c.BasisTokens = total
		c.BasisChars = contentSize(msgs)
	}
	basis, report := r.prune(&c)
	for i := range msgs {
		msgs[i] = c.Entries[i].Message
	}
	return basis, report
}

func pruneRunner() *Runner {
	r := &Runner{MaxTokens: 200000, Prune: Prune{SchemasKeep: 3}}
	if err := yaml.Unmarshal([]byte("outputs_age_tokens: 50000\n"), &r.Prune); err != nil {
		panic(err)
	}
	return r
}

func TestPruneOutputsOldestFirst(t *testing.T) {
	r := pruneRunner()
	old1 := "tool result:\n" + strings.Repeat("a", 4000)
	old2 := "tool result:\n" + strings.Repeat("b", 4000)
	fresh := "tool result:\nok"
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "user", Content: old1},
		{Role: "assistant", Content: "mid"},
		{Role: "user", Content: old2},
		{Role: "assistant", Content: "mid2"},
		{Role: "user", Content: fresh}, // unread is marked explicitly below
		{Role: "assistant", Content: strings.Repeat("r", 50001)},
	}
	basis, report := testPrune(r, msgs, contentSize(msgs), true)
	if report == "" {
		t.Fatal("expected pruning")
	}
	if !strings.Contains(msgs[2].Content, "[pruned tool output") {
		t.Errorf("oldest output kept: %q", msgs[2].Content[:40])
	}
	if !strings.Contains(msgs[4].Content, "[pruned tool output") {
		t.Errorf("middle output kept: %q", msgs[4].Content[:40])
	}
	if msgs[6].Content != fresh {
		t.Errorf("latest output touched: %q", msgs[6].Content)
	}
	if msgs[0].Content != "sys" || msgs[1].Content != "go" || msgs[3].Content != "mid" {
		t.Errorf("non-candidates touched")
	}
	// The original subsequent reasoning remains; only old output bodies shrink.
	if basis >= contentSize([]llm.Message{{Content: old1 + old2 + strings.Repeat("r", 50001)}}) {
		t.Errorf("basis=%d, old bodies did not shrink", basis)
	}
	if !strings.Contains(report, "2 tool output(s)") {
		t.Errorf("report=%q", report)
	}
}

func TestPruneSkipsUnreadLatest(t *testing.T) {
	r := pruneRunner()
	huge := "tool result:\n" + strings.Repeat("z", 9000)
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "user", Content: huge},
	}
	basis, report := testPrune(r, msgs, 1000, true)
	if report != "" || basis != 1000 {
		t.Errorf("latest must stay: basis=%d report=%q", basis, report)
	}
	if msgs[2].Content != huge {
		t.Error("latest output stubbed before the model read it")
	}
}

func TestPruneNeverTouchesPromptOrAssistant(t *testing.T) {
	r := pruneRunner()
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "tool result: not really obs"},
		{Role: "assistant", Content: "tool result:\nquoted obs"},
		{Role: "user", Content: "tail"},
	}
	if _, report := testPrune(r, msgs, 1000, true); report != "" {
		t.Errorf("report=%q", report)
	}
	if msgs[1].Content != "tool result: not really obs" || msgs[2].Content != "tool result:\nquoted obs" {
		t.Error("prompt or assistant prose stubbed")
	}
}

func TestPruneSchemasKeepAll(t *testing.T) {
	r := pruneRunner()
	r.Prune.SchemasPct = 5 // legacy percentage and count no longer prune schemas
	msgs := []llm.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "go"}}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, llm.Message{Role: "user", Content: "Schema for t:\n" + strings.Repeat("s", 4000)})
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: strings.Repeat("tail", 13000)})
	before := append([]llm.Message(nil), msgs...)
	_, report := testPrune(r, msgs, 0, false)
	if report != "" {
		t.Fatal("schema-only history should not prune", report)
	}
	for i := range msgs {
		if msgs[i].Content != before[i].Content {
			t.Fatal("routine pruning changed schema history")
		}
	}
}

func TestPruneUnknownUsesDisclosedAgeEstimate(t *testing.T) {
	r := pruneRunner()
	big := "tool result:\n" + strings.Repeat("o", 8000)
	msgs := []llm.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "go"}, {Role: "user", Content: big}, {Role: "assistant", Content: strings.Repeat("r", 50001)}}
	basis, report := testPrune(r, msgs, 0, false)
	if !strings.Contains(report, "estimated") || basis <= 50000 || msgs[2].Content == big {
		t.Errorf("consumed old output needs disclosed age estimate: basis=%d report=%q", basis, report)
	}
}

func TestPruneZeroDisables(t *testing.T) {
	r := &Runner{MaxTokens: 1000}
	big := "tool result:\n" + strings.Repeat("o", 8000)
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "go"},
		{Role: "user", Content: big},
		{Role: "user", Content: "tail"},
	}
	basis, report := testPrune(r, msgs, 1000, true)
	if report != "" || basis != 1000 || msgs[2].Content != big {
		t.Error("zero Prune must leave the transcript alone")
	}
}

func TestPruneInterplaySkipsCompact(t *testing.T) {
	// Measured 1450 (trigger 1350) but pruning drops the basis below it:
	// no compaction, one prune event, run completes. The complete catalog
	// is admitted at startup; any compaction would consume a scripted reply
	// and produce an unexpected compact event below.
	reg := tool.NewRegistry()
	var calls []string
	reg.Register(echoTool(&calls))
	model := &scriptModel{
		replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "done"},
		usages: []llm.Usage{
			{In: 20, Out: 5, Reported: true},
			{In: 1450, Out: 5, Reported: true},
		},
	}
	sink := &recordSink{}
	dir := t.TempDir()
	writeCompactionCatalog(t, dir, "UNEXPECTED_COMPACTION")
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 1500,
		SessionID: "prune-test", Session: sink,
		Compaction: testCompaction(), TemplatesDir: dir,
		Prune: Prune{OutputsAgeTokens: 1, SchemasKeep: 3}}
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if final != "done" {
		t.Fatalf("final=%q", final)
	}
	var prunes, compacts int
	for _, k := range sink.kinds {
		switch k {
		case "prune":
			prunes++
		case "compact":
			compacts++
		}
	}
	if prunes != 1 || compacts != 0 {
		t.Errorf("prunes=%d compacts=%d, want 1/0", prunes, compacts)
	}
}
