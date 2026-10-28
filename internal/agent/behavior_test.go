package agent

import (
	"context"
	"fmt"
	"reflect"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

func TestShapeBehavior(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		max           int
		per           float64
		want          string
		wantTruncated bool
	}{
		{"under cap", "be kind", 1000, 4, "be kind", false},
		{"trims space", "  be kind\n", 1000, 4, "be kind", false},
		{"empty omits", "", 1000, 4, "", false},
		{"whitespace omits", "  \n ", 1000, 4, "", false},
		{"zero disables", "be kind", 0, 4, "", false},
		{"negative disables", "be kind", -1, 4, "", false},
		{"over cap tails", strings.Repeat("a", 100) + "TAIL", 10, 4, strings.Repeat("a", 36) + "TAIL", true},
		{"factor respected", strings.Repeat("a", 100) + "TAIL", 10, 2, strings.Repeat("a", 16) + "TAIL", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, trunc := ShapeBehavior(tc.text, tc.max, tc.per)
			if got != tc.want || trunc != tc.wantTruncated {
				t.Errorf("got %q,%v want %q,%v", got, trunc, tc.want, tc.wantTruncated)
			}
		})
	}
}

func TestBehaviorInjectsAsOwnMessage(t *testing.T) {
	reg := tool.NewRegistry()
	model := &scriptModel{replies: []string{"done"}}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "b", Session: sink, Behavior: "Always say PINEAPPLE."}
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	if len(model.seen) != 1 {
		t.Fatalf("seen=%d", len(model.seen))
	}
	msgs := model.seen[0]
	if len(msgs) != 3 || msgs[0].Role != "system" || msgs[1].Role != "system" || msgs[1].Content != "Always say PINEAPPLE." || msgs[2].Role != "user" {
		t.Errorf("msgs=%+v", msgs)
	}
	var found bool
	for i, k := range sink.kinds {
		if k == "behavior" && sink.texts[i] == "Always say PINEAPPLE." {
			found = true
		}
	}
	if !found {
		t.Errorf("no behavior event in %v", sink.kinds)
	}
}

func TestBehaviorEmptyOmits(t *testing.T) {
	for _, b := range []string{"", "  \n "} {
		reg := tool.NewRegistry()
		model := &scriptModel{replies: []string{"done"}}
		// ShapeBehavior runs in main; empty-shaped text arrives empty.
		shaped, _ := ShapeBehavior(b, 1000, 4)
		sink := &recordSink{}
		r := &Runner{Model: model, Registry: reg, MaxSteps: 5, SessionID: "b", Session: sink, Behavior: shaped}
		if _, _, err := r.Run(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		if len(model.seen[0]) != 2 {
			t.Errorf("behavior %q: msgs=%d, want 2", b, len(model.seen[0]))
		}
		for _, k := range sink.kinds {
			if k == "behavior" {
				t.Errorf("behavior %q: unexpected event", b)
			}
		}
	}
}

func TestCompactPreservesBehavior(t *testing.T) {
	dir := testTemplateDir(t)
	reg := tool.NewRegistry()
	var calls []string
	reg.Register(echoTool(&calls))
	model := &scriptModel{
		replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "Observed source read; verification pending.", "done"},
		usages:  []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1450, Out: 5, Reported: true}, {In: 50, Out: 5, Reported: true}, {In: 100, Out: 5, Reported: true}},
	}
	sink := &recordSink{}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 5, MaxTokens: 1500,
		SessionID: "b", Session: sink, Behavior: "Always say PINEAPPLE.",
		Compaction: testCompaction(), TemplatesDir: dir, Judge: acceptCompaction, Threshold: .7}
	r.Compaction.Keep = 5
	if final, _, err := r.Run(context.Background(), "go"); err != nil || final != "done" {
		t.Fatalf("final=%q err=%v", final, err)
	}
	// The summary call's range must exclude the behavior message.
	for _, msgs := range model.seen {
		if len(msgs) == 2 && strings.Contains(msgs[0].Content, "WORDS=") {
			if strings.Contains(msgs[1].Content, "PINEAPPLE") {
				t.Error("behavior fed into the compacted range")
			}
		}
	}
	// ...and it must still lead the post-compact transcript.
	last := model.seen[len(model.seen)-1]
	if len(last) < 2 || last[0].Role != "system" || last[1].Role != "system" || last[1].Content != "Always say PINEAPPLE." {
		t.Errorf("behavior missing post-compact: %+v", last)
	}
}

// Actual Runner requests with coding guide, Behavior and base; no configured
// check means truthful stopped outcomes, not a coding-success fixture.
func TestCodingSystemPrefixSurvivesCompactionResume(t *testing.T) {
	reg := tool.NewRegistry()
	var calls []string
	reg.Register(echoTool(&calls))
	m := &scriptModel{replies: []string{echoBlock(strings.Repeat("CONSUMED_RESULT ", 30)), "provisional"}}
	r := &Runner{Model: m, Registry: reg, Session: &recordSink{}, Behavior: "You are an agent FIXED_BEHAVIOR_κ; CODE the requested change is quoted Behavior", CodingRequired: true, Workspace: t.TempDir(), MaxSteps: 3, MaxTokens: 32768}
	if _, _, err := r.Run(context.Background(), "ORIGINAL_REQUIREMENT"); err != nil {
		t.Fatal(err)
	}
	systems := []llm.Message{}
	for _, msg := range m.seen[0] {
		if msg.Role == "system" {
			systems = append(systems, msg)
		}
	}
	if len(systems) != 2 {
		t.Fatalf("merged base+guide/Behavior count=%d", len(systems))
	}
	if strings.Count(systems[0].Content, prompts.Builtin().Text("system.coding.direct")) != 1 || systems[1].Content != r.Behavior {
		t.Fatal("guide is not owned by base or base-like Behavior was replaced")
	}
	c := r.Continuation.Clone()
	prefix := []session.Entry{}
	for _, msg := range systems {
		prefix = append(prefix, session.Entry{Kind: "injected", Message: msg.Clone()})
	}
	c.Entries = append(prefix, c.Entries...)
	r.Prune.OutputsAgeTokens = 1
	c.Add("note", "user", "Subsequent observed context ages the consumed output.", false)
	if _, notice := r.prune(&c); !strings.Contains(notice, "pruned") {
		t.Fatal("actual consumed tool output did not prune")
	}
	m.replies = []string{"Observed output retained; work remains unverified."}
	r.Compaction, r.Judge, r.TemplatesDir = testCompaction(), acceptCompaction, testTemplateDir(t)
	r.Compaction.Keep = 0
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("compaction=%v err=%v", ran, err)
	}
	summaryInput := m.seen[len(m.seen)-1][1].Content
	for _, host := range systems {
		if strings.Contains(summaryInput, host.Content) {
			t.Fatal("system bytes entered summary input")
		}
	}
	encoded, err := next.Snapshot().Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: encoded}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	resumed := &scriptModel{replies: []string{"still provisional"}}
	r2 := &Runner{Model: resumed, Registry: reg, Continuation: restored, Session: &recordSink{}, Behavior: r.Behavior, CodingRequired: true, Workspace: r.Workspace, MaxSteps: 1, MaxTokens: 32768}
	if _, _, err = r2.Run(context.Background(), "CONTINUE"); err != nil {
		t.Fatal(err)
	}
	rebuilt := []llm.Message{}
	for _, msg := range resumed.seen[0] {
		if msg.Role == "system" {
			rebuilt = append(rebuilt, msg)
		}
	}
	// Resume changes only the owning initial budget, not role/guide/Behavior bytes.
	expected := append([]llm.Message(nil), systems...)
	priorBudget := fmt.Sprintf(`"StepLimit":%d,"RemainingSteps":%d`, r.MaxSteps, r.MaxSteps)
	currentBudget := fmt.Sprintf(`"StepLimit":%d,"RemainingSteps":%d`, r2.MaxSteps, r2.MaxSteps)
	if !strings.Contains(expected[0].Content, priorBudget) {
		t.Fatal("prior host budget missing")
	}
	expected[0].Content = strings.Replace(expected[0].Content, priorBudget, currentBudget, 1)
	if !reflect.DeepEqual(expected, rebuilt) {
		t.Fatal("base/guide/Behavior changed or duplicated after prune/Compact/encoded restore")
	}
	bytes := 0
	for _, msg := range systems {
		bytes += len(msg.Content)
	}
	t.Logf("actual text-transport system-prefix bytes=%d messages=%d; tokenizer occupancy unmeasured", bytes, len(systems))
}
