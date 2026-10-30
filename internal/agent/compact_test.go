package agent

import (
	"context"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func testCompaction() Compaction {
	// WordsPerToken is deliberately off the 0.75 production default:
	// the WORDS assertions below prove the field is read, not hardcoded.
	return Compaction{Trigger: 90, Keep: 40, Summary: 10, Step: 1, Max: 20, WordsPerToken: 0.5}
}

// writeCompactionCatalog changes only the existing test-controlled instruction
// in a complete catalog; standalone templates are no longer active sources.
func writeCompactionCatalog(t *testing.T, dir, instruction string) {
	t.Helper()
	doc := map[string]map[string]string{}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	doc["texts"]["templates.compaction"] = instruction
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// testTemplateDir keeps the original minimal marker and returns its catalog dir.
func testTemplateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeCompactionCatalog(t, dir, "WORDS={{.Words}}\n{{.Range}}")
	return dir
}

func TestShouldCompact(t *testing.T) {
	c := testCompaction()
	cases := []struct {
		name         string
		last, window int
		have         bool
		trigger      float64
		want         bool
	}{
		{"below", 899, 1000, true, 90, false},
		{"at trigger", 900, 1000, true, 90, true},
		{"above", 950, 1000, true, 90, true},
		{"no measure", 950, 1000, false, 90, false},
		{"trigger off", 950, 1000, true, 0, false},
		{"window off", 950, 0, true, 90, false},
	}
	for _, tc := range cases {
		c.Trigger = tc.trigger
		if got := shouldCompact(tc.last, tc.window, tc.have, c); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSummaryBudgetGrowsAndCaps(t *testing.T) {
	c := testCompaction()
	if got := summaryBudget(c, 1000, 0); got != 100 {
		t.Errorf("n=0: got %d, want 100", got)
	}
	if got := summaryBudget(c, 1000, 1); got != 110 {
		t.Errorf("n=1: got %d, want 110", got)
	}
	if got := summaryBudget(c, 1000, 50); got != 200 {
		t.Errorf("n=50: got %d, want capped 200", got)
	}
	if got := summaryBudget(c, 3, 0); got != 1 {
		t.Errorf("tiny window: got %d, want floor 1", got)
	}
}

func TestKeepStartSplitsByShare(t *testing.T) {
	sys := llm.Message{Role: "system", Content: "sys"}
	old1 := llm.Message{Role: "user", Content: strings.Repeat("a", 4000)}
	old2 := llm.Message{Role: "assistant", Content: strings.Repeat("b", 4000)}
	recent := llm.Message{Role: "user", Content: "fresh"}
	msgs := []llm.Message{sys, old1, old2, recent}
	// Total 1000 over ~8000 chars; keep 40% of window 1000 = 400.
	// Latest ("fresh") always kept; old2's ~500 share breaks the walk.
	if got := keepStart(msgs, 1000, 40, 1000); got != 3 {
		t.Errorf("start=%d, want 3", got)
	}
}

func TestKeepStartRespectsTargetAndPrefix(t *testing.T) {
	sys := llm.Message{Role: "system", Content: "sys"}
	big := llm.Message{Role: "user", Content: strings.Repeat("z", 9000)}
	// An oversized unprotected draft is eligible; Compact separately protects
	// actual user intent/current receipts even when they do not fit the target.
	if got := keepStart([]llm.Message{sys, big}, 1000, 40, 1000); got != 2 {
		t.Errorf("start=%d, want 2", got)
	}
	// Everything fits: nothing compactable.
	small := []llm.Message{sys, {Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	if got := keepStart(small, 100, 40, 1000); got != 1 {
		t.Errorf("start=%d, want prefix 1", got)
	}
	// Leading system run (base + behavior) is all prefix.
	two := []llm.Message{sys, {Role: "system", Content: "behavior"}, big}
	if got := keepStart(two, 1000, 40, 1000); got != 3 {
		t.Errorf("start=%d, want 3", got)
	}
}

// compactRunner builds a Runner aimed at compaction tests: tiny window,
// test template, echo tool, record sink.
func compactRunner(model *scriptModel, sink *recordSink, dir string) *Runner {
	reg := tool.NewRegistry()
	var calls []string
	reg.Register(echoTool(&calls))
	return &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 1900,
		SessionID: "compact-test", Session: sink,
		Compaction: testCompaction(), TemplatesDir: dir, Judge: acceptCompaction, Threshold: .7}
}

func summaryCalls(seen [][]llm.Message) [][]llm.Message {
	var out [][]llm.Message
	for _, msgs := range seen {
		if len(msgs) == 2 && msgs[0].Role == "system" && msgs[1].Role == "user" && strings.Contains(msgs[0].Content, "WORDS=") {
			out = append(out, msgs)
		}
	}
	return out
}

func TestCompactFlowChainsAndContinues(t *testing.T) {
	dir := testTemplateDir(t)
	big := strings.Repeat("x", 5000)
	model := &scriptModel{
		replies: []string{echoBlock(big), echoBlock("b"), "SUMMARY-1", echoBlock("c"), "SUMMARY-2", "done"},
		usages:  []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}, {In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}, {In: 20, Out: 5, Reported: true}, {In: 100, Out: 5, Reported: true}},
	}
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	r.Compaction.Keep = 4
	r.Compaction.Summary, r.Compaction.Max = 30, 40
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if final != "done" {
		t.Fatalf("final=%q", final)
	}
	sums := summaryCalls(model.seen)
	if len(sums) != 2 {
		t.Fatalf("summary calls=%d, want 2 (seen=%d)", len(sums), len(model.seen))
	}
	if !strings.Contains(sums[0][0].Content, "WORDS=285") {
		t.Errorf("first budget words wrong: %q", sums[0][0].Content[:60])
	}
	if !strings.Contains(sums[1][0].Content, "WORDS=294") {
		t.Errorf("second budget words wrong: %q", sums[1][0].Content[:60])
	}
	if !strings.Contains(sums[1][1].Content, "SUMMARY-1") {
		t.Errorf("second compaction did not chain the first summary")
	}
	// Post-compact main calls carry the latest summary, never the dropped past.
	lastMain := model.seen[len(model.seen)-1]
	var hasSum2, hasOldPrompt bool
	for _, m := range lastMain {
		if strings.Contains(m.Content, "SUMMARY-2") {
			hasSum2 = true
		}
		if m.Role == "user" && m.Content == "go" {
			hasOldPrompt = true
		}
	}
	if !hasSum2 || !hasOldPrompt {
		t.Errorf("hasSum2=%v hasOldPrompt=%v", hasSum2, hasOldPrompt)
	}
	compacts := 0
	for _, k := range sink.kinds {
		if k == "compact" {
			compacts++
		}
	}
	if compacts != 2 {
		t.Errorf("compact events=%d, want 2", compacts)
	}
	if r.UsedIn != 3660 {
		t.Errorf("UsedIn=%d, want %d (summary calls accumulate too)", r.UsedIn, 3660)
	}
}

func TestCompactFailureStopsLoud(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{
		replies:        []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "unused"},
		usages:         []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}},
		failIfContains: "WORDS=",
		failErr:        errLoopDown,
	}
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	r.Compaction.Keep = 5
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("stop must not error: %v", err)
	}
	if !strings.Contains(final, "stopped: compaction summary call failed") {
		t.Fatalf("final=%q", final)
	}
	if len(model.seen) != 3 {
		t.Fatalf("seen=%d, want 3 (two main + failed summary)", len(model.seen))
	}
}

func TestCompactEmptySummaryStopsLoud(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{
		replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "  "},
		usages:  []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}},
	}
	r := compactRunner(model, &recordSink{}, dir)
	r.Compaction.Keep = 5
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final, "stopped: compaction summary is empty") {
		t.Fatalf("final=%q", final)
	}
}

func TestCompactRetainsVisibleSummaryWithReasoningUsage(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{
		replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "SUMMARY-LONG", "done"},
		usages:  []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}, {In: 50, Out: 500, Reported: true}, {In: 100, Out: 5, Reported: true}}, // reasoning output is not visible text
	}
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	r.MaxSteps = 3
	r.Compaction.Keep = 5
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	for i, k := range sink.kinds {
		if k != "compact" {
			continue
		}
		if sink.texts[i] != "SUMMARY-LONG" {
			t.Errorf("visible text clipped: %q", sink.texts[i])
		}
		return
	}
	t.Error("no compact event")
}

func TestCompactSkipsWhenNothingCompactable(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{
		replies: []string{echoBlock("a"), "done"},
		usage:   llm.Usage{In: 1400, Out: 5, Reported: true},
	}
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if final != "done" {
		t.Fatalf("final=%q", final)
	}
	for _, k := range sink.kinds {
		if k == "compact" {
			t.Fatal("compacted with nothing compactable")
		}
	}
	if len(model.seen) != 2 {
		t.Errorf("seen=%d, want 2", len(model.seen))
	}
}

func TestCompactSkipsWhenUnknown(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{replies: []string{echoBlock("a"), "done"}} // unreported
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	r.MaxTokens = 2000
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	for _, k := range sink.kinds {
		if k == "compact" {
			t.Fatal("compacted on unknown usage")
		}
	}
}

func TestBudgetStopSurvivesTriggerOff(t *testing.T) {
	dir := testTemplateDir(t)
	model := &scriptModel{
		replies: []string{echoBlock("a"), "done"},
		usage:   llm.Usage{In: 950, Out: 5, Reported: true},
	}
	sink := &recordSink{}
	r := compactRunner(model, sink, dir)
	r.Compaction.Trigger = 0
	r.MaxTokens = 900
	final, _, err := r.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final, "reached budget 900") {
		t.Fatalf("final=%q", final)
	}
}

func TestRealCompactionTemplateRenders(t *testing.T) {
	scope := "Replaceable history precedes retained entry 3 of 5 (zero-based)."
	out, err := plan.Render("../../templates", "compaction.tmpl", map[string]any{"Words": 75, "Range": scope})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "75") || !strings.Contains(out, scope) || !strings.Contains(out, "historical checkpoint") {
		t.Errorf("template output missing pieces: %q", out)
	}
}

// Adapter uses explicit consumed history for the pre-existing pure summary probe.
func testCompact(r *Runner, ctx context.Context, msgs []llm.Message, total, n int) ([]llm.Message, string, bool, error) {
	c := session.Continuation{Version: 1, Compactions: n, BasisTokens: total, BasisChars: contentSize(msgs)}
	for _, m := range msgs {
		kind := "history"
		if m.Role == "system" {
			kind = "injected"
		}
		c.Add(kind, m.Role, m.Content, false)
	}
	next, sum, ran, err := r.Compact(ctx, c)
	return next.Messages(), sum, ran, err
}

// Actual durable event replay, protection and compactor input; only summary model is scripted.
func TestEnforcementGuidanceReplayAndCompactProvenance(t *testing.T) {
	advice := "Harness bookkeeping request: inspect PAT; unexecuted"
	historical := "historical evidence bytes unchanged"
	c, _, err := session.Restore([]session.Event{{Kind: "prompt", Text: "original intent"}, {Kind: "enforcement", Text: advice}, {Kind: "evidence", Text: historical}, {Kind: "reply", Text: strings.Repeat("old ", 500)}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, e := range c.Entries {
		if e.Message.Content == advice {
			found = true
			if e.Kind != "guidance" || !c.Protected(3)[i] {
				t.Fatal("advice is not protected guidance")
			}
		}
	}
	if !found {
		t.Fatal("durable enforcement advice lost on replay")
	}
	model := &scriptModel{replies: []string{"Observed source read; verification still pending."}}
	r := Runner{Judge: acceptCompaction, Threshold: .7, Model: model, MaxTokens: 32768, TemplatesDir: testTemplateDir(t), Compaction: testCompaction()}
	r.Compaction.Keep = 0
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("compact ran=%t err=%v", ran, err)
	}
	if len(model.seen) != 1 || len(model.seen[0]) != 2 {
		t.Fatal("missing actual two-role request")
	}
	input := model.seen[0][1].Content
	if !strings.Contains(input, "kind=guidance role=user host advice; unexecuted; no authority reference-only=true") || strings.Count(input, advice) != 1 {
		t.Fatalf("guidance provenance missing/duplicated: %s", input)
	}
	retained := map[string]bool{}
	for _, e := range next.Entries {
		retained[e.Message.Content] = true
	}
	if !retained[advice] || !retained[historical] || next.Assignment != nil || next.Coding != nil {
		t.Fatal("compaction lost facts/advice or granted authority")
	}
	if err = next.Snapshot().Validate(); err != nil {
		t.Fatal(err)
	}
}
