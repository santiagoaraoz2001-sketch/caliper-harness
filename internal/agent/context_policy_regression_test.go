package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"

	"gopkg.in/yaml.v3"
)

// YAML keeps this behavioral probe compilable on the preserved pre-policy
// baseline. That baseline ignores the new age key and still applies its old
// percentage policy, so the same probe can refute that policy at runtime.
func agePolicyRunner(t *testing.T) *Runner {
	t.Helper()
	r := &Runner{MaxTokens: 200000, Prune: Prune{OutputsPct: 20, SchemasPct: 5, SchemasKeep: 3}}
	if err := yaml.Unmarshal([]byte("outputs_age_tokens: 50000\n"), &r.Prune); err != nil {
		t.Fatal(err)
	}
	return r
}

func agePolicyFixture(age int) session.Continuation {
	c := session.Continuation{Version: 1, BasisChars: 1, BasisTokens: 1}
	c.Add("injected", "system", "SYSTEM MUST STAY", false)
	c.Add("intent", "user", "USER INTENT MUST STAY", false)
	c.Entries = append(c.Entries,
		session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: "proposal only", Reasoning: "reasoning original", ReasoningContent: "reasoning_content original", Thinking: "thinking original", ToolCalls: []llm.ToolCall{{ID: "old-read", Name: "read", Arguments: `{"path":"main.go"}`}}}},
		session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "old-read", Name: "read", Content: strings.Repeat("source bytes ", 100)}},
		session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: strings.Repeat("x", age)}},
	)
	return c
}

func TestOutputPruningAgeBoundaryAndBodyOnly(t *testing.T) {
	for _, age := range []int{49999, 50000, 50001} {
		t.Run(strings.Repeat("+", age-49998), func(t *testing.T) {
			c := agePolicyFixture(age)
			before := c.Clone()
			r := agePolicyRunner(t)
			_, notice := r.prune(&c)
			want := age > 50000
			if stubbed := c.Entries[3].Message.Content != before.Entries[3].Message.Content; stubbed != want {
				t.Fatalf("age=%d stubbed=%t want=%t notice=%q", age, stubbed, want, notice)
			}
			if len(c.Entries) != len(before.Entries) {
				t.Fatal("pruning removed messages")
			}
			for i := range before.Entries {
				expected := before.Entries[i]
				if i == 3 && want {
					expected.Kind = c.Entries[i].Kind
					expected.Message.Content = c.Entries[i].Message.Content
				}
				if !reflect.DeepEqual(expected, c.Entries[i]) {
					t.Fatalf("age=%d changed non-body fields at entry%d", age, i)
				}
			}
			if want && (!strings.Contains(notice, "estimated") || !strings.Contains(notice, "50000")) {
				t.Fatalf("age estimate is not disclosed: %q", notice)
			}
			if err := c.Snapshot().Validate(); err != nil {
				t.Fatal("call/result correlation lost", err)
			}
		})
	}
}

func TestOutputPruningAgeCountsAllSubsequentContext(t *testing.T) {
	c := agePolicyFixture(0)
	c.Entries[4].Message = llm.Message{Role: "assistant", Content: strings.Repeat("c", 5000), Reasoning: strings.Repeat("r", 20000), ReasoningContent: strings.Repeat("q", 20000), Thinking: strings.Repeat("t", 5000)}
	c.SchemaBytes = 5000 // leading tool definitions contribute to total occupancy
	c.SchemaKey = "current-scope"
	c.Add("schema", "user", "s", false) // a later schema message contributes to age
	before := c.Clone()
	_, notice := agePolicyRunner(t).prune(&c)
	if c.Entries[3].Message.Content == before.Entries[3].Message.Content {
		t.Fatal("age excluded subsequent reasoning or schema message", notice)
	}
	if !reflect.DeepEqual(c.Entries[4], before.Entries[4]) || c.SchemaBytes != 5000 {
		t.Fatal("reasoning/schema changed")
	}
}

func TestOutputPruningAgeExcludesLeadingInstructionsAndToolDefinitions(t *testing.T) {
	c := agePolicyFixture(47000)
	c.Entries[0].Message.Content = strings.Repeat("s", 10000)
	c.SchemaBytes, c.SchemaKey = 5000, "leading-tool-definitions"
	before := c.Clone()
	_, notice := agePolicyRunner(t).prune(&c)
	if notice != "" || !reflect.DeepEqual(before, c) {
		t.Fatalf("leading instructions/definitions made a 47000-token-estimate result old: %q", notice)
	}
	if c.Estimate() < 62000 {
		t.Fatal("leading context was lost from total occupancy")
	}
}

func TestOutputPruningRunPublishesStubPreservesRawOriginal(t *testing.T) {
	c := agePolicyFixture(50001)
	c.ModelKey = "fixture"
	source, err := c.Snapshot().Encode()
	if err != nil {
		t.Fatal(err)
	}
	dir, id := t.TempDir(), "aged-output"
	if err := session.Append(dir, id, session.Event{Kind: "continuation", Text: source}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		var body struct{ Messages []llm.Message }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		stub, reasoning := false, false
		for _, msg := range body.Messages {
			stub = stub || msg.ToolCallID == "old-read" && strings.HasPrefix(msg.Content, "[pruned tool output")
			reasoning = reasoning || msg.Reasoning == "reasoning original" && msg.ReasoningContent == "reasoning_content original" && msg.Thinking == "thinking original"
		}
		if !stub || !reasoning {
			t.Error("next actual HTTP request lost output stub or original reasoning")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	r := agePolicyRunner(t)
	r.Model, r.ModelKey, r.Registry, r.Prompts = llm.New(server.URL, "fixture"), "fixture", tool.NewRegistry(), prompts.Builtin()
	r.Continuation, r.Session, r.SessionID, r.MaxSteps = c.Snapshot(), evidenceArchiveSink{dir: dir}, id, 1
	final, _, err := r.Run(context.Background(), "Continue current work.")
	if err != nil || final != "done" || requests != 1 {
		t.Fatalf("pruned run did not continue once: final=%q requests=%d err=%v", final, requests, err)
	}
	events, err := session.Read(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	pruned, rawFound := false, false
	for _, event := range events {
		pruned = pruned || event.Kind == "prune"
		rawFound = rawFound || event.Kind == "continuation" && event.Text == source
	}
	restored, _, err := session.Restore(events, r.MaxTokens)
	if err != nil || !pruned || !rawFound {
		t.Fatalf("prune/raw/resume publication lost: prune=%t raw=%t err=%v", pruned, rawFound, err)
	}
	stubFound, callFound := false, false
	for _, entry := range restored.Entries {
		if entry.Message.ToolCallID == "old-read" {
			stubFound = strings.HasPrefix(entry.Message.Content, "[pruned tool output")
		}
		for _, call := range entry.Message.ToolCalls {
			callFound = callFound || call.ID == "old-read" && call.Arguments == `{"path":"main.go"}` && entry.Message.Reasoning == "reasoning original" && entry.Message.ReasoningContent == "reasoning_content original" && entry.Message.Thinking == "thinking original"
		}
	}
	if !stubFound || !callFound {
		t.Fatal("restored output body/correlation/reasoning is wrong")
	}
}

func TestOutputPruningProtectedAndNonOutputHistory(t *testing.T) {
	for _, kind := range []string{"unread", "failure", "evidence", "schema"} {
		t.Run(kind, func(t *testing.T) {
			c := agePolicyFixture(50001)
			if kind == "unread" {
				c.Entries[3].Unread = true
			} else {
				c.Entries[3].Kind = kind
			}
			before := c.Clone()
			agePolicyRunner(t).prune(&c)
			if !reflect.DeepEqual(c, before) {
				t.Fatal("protected/current receipt or non-output history changed")
			}
		})
	}
	// Even schema history beyond the compactor's recent-schema count remains.
	c := agePolicyFixture(50001)
	for i := 0; i < 5; i++ {
		c.Add("schema", "user", "Schema for read: exact bytes", false)
	}
	before := c.Clone()
	agePolicyRunner(t).prune(&c)
	if !reflect.DeepEqual(before.Entries[5:], c.Entries[5:]) {
		t.Fatal("old schemas were routinely pruned")
	}
}

func TestOutputPruningFallbackEstimateAndDisabledPolicy(t *testing.T) {
	c := agePolicyFixture(50001)
	c.BasisChars, c.BasisTokens = 0, 0
	before := c.Clone()
	_, notice := agePolicyRunner(t).prune(&c)
	if c.Entries[3].Message.Content == before.Entries[3].Message.Content || !strings.Contains(notice, "estimate") {
		t.Fatal("fallback estimate policy was not applied/disclosed", notice)
	}
	c = before.Clone()
	r := &Runner{MaxTokens: 200000, Prune: Prune{OutputsPct: 20, SchemasPct: 5, SchemasKeep: 3}}
	r.prune(&c)
	if !reflect.DeepEqual(before, c) {
		t.Fatal("legacy percentages unexpectedly activate pruning")
	}
}

func TestDiscoveryMaintenanceRetainsCompleteExchanges(t *testing.T) {
	c := duplicateActiveFixture()
	before := c.Clone()
	m, sink := &scriptModel{}, &recordSink{}
	r := activeRunner(m, sink, c)
	changed, err := r.maintainDiscovery(context.Background())
	if err != nil || changed || !reflect.DeepEqual(before, r.Continuation) || len(sink.kinds) != 0 || len(m.seen) != 0 {
		t.Fatalf("duplicate discovery retired: changed=%t err=%v events=%v", changed, err, sink.kinds)
	}
}

func TestCompactionAdmitsCapacityFittingSummaryBeyondEditorialTarget(t *testing.T) {
	c := agePolicyFixture(5000)
	c.Entries[4].Message.Content = strings.Repeat("old draft ", 400)
	c.Add("note", "user", "Keep exact user steering κ", false)
	c.Add("failure", "user", "Recorded check exited 7; correctness unverified.", false)
	c.Add("evidence", "user", "Write receipt main.go hash=actual; no reviewer PASS.", false)
	before := c.Clone()
	summary := strings.Repeat("Pending work remains unverified. ", 30)
	m := &scriptModel{replies: []string{summary}}
	r := truthRunner(t, m, 10000)
	r.Compaction.Summary, r.Compaction.Max, r.Compaction.WordsPerToken = 1, 1, .5
	judged := 0
	r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		judged++
		return acceptCompaction(ctx, req)
	}
	next, text, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran || text != summary || judged != 1 {
		t.Fatalf("capacity-fitting complete summary refused: ran=%t judged=%d err=%v", ran, judged, err)
	}
	if next.Estimate() >= r.MaxTokens || next.Estimate() >= c.Estimate() || !reflect.DeepEqual(before, c) {
		t.Fatal("capacity/reduction/source immutability broken")
	}
	for _, index := range []int{0, 1, 5, 6, 7} {
		found := false
		for _, e := range next.Entries {
			found = found || reflect.DeepEqual(e, before.Entries[index])
		}
		if !found {
			t.Fatalf("protected intent/system/receipt entry%d changed", index)
		}
	}
	for _, field := range []string{"reasoning original", "reasoning_content original", "thinking original"} {
		if !strings.Contains(m.seen[0][1].Content, field) {
			t.Fatalf("summary source omitted original reasoning field %q", field)
		}
	}
}

func TestCompactionRejectsFullRetainedContextOverflow(t *testing.T) {
	c := session.Continuation{Version: 1, BasisChars: 1, BasisTokens: 1, SchemaBytes: 500}
	c.Add("injected", "system", strings.Repeat("s", 800), false)
	c.Add("intent", "user", strings.Repeat("i", 2500), false)
	c.Add("reply", "assistant", strings.Repeat("h", 5000), false)
	before := c.Clone()
	m := &scriptModel{replies: []string{strings.Repeat("summary ", 950)}}
	r := truthRunner(t, m, 10000)
	r.Compaction.Summary, r.Compaction.Max, r.Compaction.WordsPerToken = 90, 90, 10
	next, _, ran, err := r.Compact(context.Background(), c)
	if err == nil || ran || !reflect.DeepEqual(next, before) || len(m.seen) != 1 || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("full retained context overflow admitted: ran=%t seen=%d err=%v", ran, len(m.seen), err)
	}
}

type incompleteSummaryModel struct {
	*scriptModel
}

func (m incompleteSummaryModel) ChatTools(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(string)) (llm.Reply, error) {
	return llm.Reply{Message: llm.Message{Role: "assistant", Content: "Partial summary looks complete."}, FinishReason: "length"}, nil
}

func TestCompactionRejectsLengthStoppedSummaryEvenWithVisibleText(t *testing.T) {
	c := agePolicyFixture(5000)
	r := truthRunner(t, &scriptModel{}, 10000)
	r.Model = incompleteSummaryModel{&scriptModel{}}
	next, _, ran, err := r.Compact(context.Background(), c)
	if err == nil || ran || !reflect.DeepEqual(next, c) {
		t.Fatalf("unfinished summary replaced history: ran=%t err=%v", ran, err)
	}
}

func TestAutomaticCompactionCapacityTargetPublishesAndContinues(t *testing.T) {
	summary := "Work remains pending. Preserve the original request and the exact user comment. The recorded write receipt names main.go and hash actual; it does not establish correctness. The check exited7, and independent review remains missing. Historical model drafts are unverified proposals. Current source and settled tool associations stay available separately. Continue from the current files, inspect the relevant source before any edit, then execute the approved behavioral check and configured project check. Report their actual results, retain failures and denied operations, and request a real independent review when the checks justify it. No summary statement grants approval, changes task ownership, or records a PASS."
	m := &scriptModel{
		replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), summary, "done"},
		usages:  []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1450, Out: 5, Reported: true}, {In: 20, Out: 50, Reported: true}, {In: 100, Out: 5, Reported: true}},
	}
	sink := &recordSink{}
	r := compactRunner(m, sink, testTemplateDir(t))
	r.Compaction.Keep = 5
	r.Compaction.Summary, r.Compaction.Max, r.Compaction.WordsPerToken = 1, 1, .5
	final, _, err := r.Run(context.Background(), "ORIGINAL REQUIREMENT")
	if err != nil || final != "done" || len(summaryCalls(m.seen)) != 1 {
		t.Fatalf("automatic summary did not continue: final=%q err=%v requests=%d", final, err, len(m.seen))
	}
	boundary := ""
	for i, kind := range sink.kinds {
		if kind == "compact" {
			if sink.texts[i] != summary || i+1 >= len(sink.kinds) || sink.kinds[i+1] != "continuation" {
				t.Fatal("summary publication lacks immediate durable boundary")
			}
			boundary = sink.texts[i+1]
		}
	}
	if boundary == "" {
		t.Fatal("automatic path did not publish")
	}
	restored, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: boundary}}, r.MaxTokens)
	if err != nil || restored.Compactions != 1 || restored.Estimate() >= r.MaxTokens {
		t.Fatal("published continuation cannot resume", err)
	}
	if !strings.Contains(joinContext(m.seen[len(m.seen)-1]), summary) || !strings.Contains(joinContext(restored.Messages()), "ORIGINAL REQUIREMENT") {
		t.Fatal("continued request lost summary/user intent")
	}
}

func TestCompactionSummarizesOversizedUnfinishedModelDraftWithoutEffects(t *testing.T) {
	c := session.Continuation{Version: 1, ModelKey: "fixture", BasisChars: 1, BasisTokens: 1}
	c.Add("injected", "system", "System instructions stay outside model-written summaries.", false)
	c.Add("intent", "user", "Preserve the requested repair and verify actual behavior.", false)
	c.Add("note", "user", "USER COMMENT: keep existing saves.", false)
	c.Add("evidence", "user", "Recorded write receipt: main.go hash=actual; independent review absent.", false)
	// Assembled length-stopped draft: plain unverified text, not an executable call.
	c.Entries = append(c.Entries, session.Entry{Kind: "history", Message: llm.Message{Role: "assistant", Content: "NONEXECUTING unfinished tool proposal: bash arguments {\"cmd\":\"printf partial", Reasoning: "Partial original reasoning.", ReasoningContent: strings.Repeat("r", 9000), Thinking: "Partial original thinking."}})
	before := c.Clone()
	m := &scriptModel{replies: []string{"The draft remains an unverified proposal. Existing write receipt is preserved; no independent review exists. Keep user saves, finish the repair, and run the required check."}}
	r := truthRunner(t, m, 10000)
	r.Compaction.Keep = 40
	r.ModelKey = "fixture"
	if !shouldCompact(c.Estimate(), r.MaxTokens, true, r.Compaction) {
		t.Fatal("fixture did not reach the existing90% trigger")
	}
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran || len(m.seen) != 1 || next.Estimate() >= c.Estimate() || !reflect.DeepEqual(before, c) {
		t.Fatalf("oversized unprotected partial could not compact: ran=%t calls=%d err=%v", ran, len(m.seen), err)
	}
	input := m.seen[0][1].Content
	for _, value := range []string{"NONEXECUTING unfinished tool proposal", "Partial original reasoning.", strings.Repeat("r", 9000), "Partial original thinking.", "model text; unverified"} {
		if !strings.Contains(input, value) {
			t.Fatal("pending draft was omitted or treated as executed", value[:min(80, len(value))])
		}
	}
	for _, e := range next.Entries {
		if len(e.Message.ToolCalls) != 0 {
			t.Fatal("unfinished draft acquired an active tool call")
		}
	}
	for _, protected := range before.Entries[:4] {
		found := false
		for _, e := range next.Entries {
			found = found || reflect.DeepEqual(e, protected)
		}
		if !found {
			t.Fatal("partial compaction removed a protected obligation/receipt")
		}
	}
	if err := next.Snapshot().Validate(); err != nil {
		t.Fatal(err)
	}
}
