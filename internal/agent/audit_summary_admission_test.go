package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestVacuousSummaryPreservesContextAndDoesNotRepeatWork(t *testing.T) {
	for _, summary := range []string{"", "Checkpoint.", "Summary: work has been completed successfully."} {
		t.Run(summary, func(t *testing.T) {
			original := passedCompactionFixture()
			model := &scriptModel{replies: []string{summary}}
			r := truthRunner(t, model, 32768)
			judgments := 0
			r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
				judgments++
				return decision.Verdict{Option: q.Options[0], Confidence: .99}, nil
			}
			for i := 0; i < 2; i++ {
				next, _, ran, err := r.Compact(t.Context(), original)
				if err == nil || ran || !reflect.DeepEqual(next, original) {
					t.Fatalf("accepted placeholder or changed original: %v", err)
				}
			}
			if len(model.seen) != 1 || judgments != 0 {
				t.Fatalf("repeated unusable work: calls=%d judgments=%d", len(model.seen), judgments)
			}
		})
	}
	if !summaryHasContent("Parser test failed; preserve the user's chosen font.") {
		t.Fatal("short factual content refused")
	}
}

func TestCompactionNativeCapacityReusesCandidateAndProtectsFacts(t *testing.T) {
	c := passedCompactionFixture()
	for i := 0; i < 20; i++ {
		c.Add("evidence", "user", strings.Repeat("older host receipt; ", 30), false)
	}
	c.Add("evidence", "user", "LATEST ACTUAL RECEIPT", false)
	m := &scriptModel{replies: []string{"Converter verification passed; fail.sh exited7; user note retained."}}
	r := truthRunner(t, m, 32768)
	calls, before := 0, 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		if calls == 1 {
			before = len(q.Context)
			return decision.Verdict{}, &decision.ContextCapacityError{Message: "request (16849 tokens) exceeds the available context size (16384 tokens), try increasing it"}
		}
		if len(q.Context) >= before || !strings.Contains(q.Context, "LATEST ACTUAL RECEIPT") || !strings.Contains(q.Context, "preserve") || !strings.Contains(q.Context, "RetainedOutsideReview") {
			t.Fatal("projection erased mandatory facts or hid omission")
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .9}, nil
	}
	next, _, ran, err := r.Compact(t.Context(), c)
	if err != nil || !ran || calls != 2 || len(m.seen) != 1 {
		t.Fatalf("candidate reuse failed: ran=%v calls=%d model=%d err=%v", ran, calls, len(m.seen), err)
	}
	if len(next.Entries) < 21 {
		t.Fatal("transport projection deleted retained host records")
	}
}

func TestRejectedCompactionCandidateIsNotRejudged(t *testing.T) {
	c := passedCompactionFixture()
	m := &scriptModel{replies: []string{"The converter check remains unverified."}}
	r := truthRunner(t, m, 32768)
	calls := 0
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		return decision.Verdict{Option: q.Options[1], Confidence: .9}, nil
	}
	for i := 0; i < 2; i++ {
		next, _, ran, err := r.Compact(t.Context(), c)
		if err != nil || ran || !reflect.DeepEqual(next, c) {
			t.Fatal("rejection changed original", err)
		}
	}
	if calls != 1 || len(m.seen) != 1 {
		t.Fatalf("repeated rejected work: judge=%d model=%d", calls, len(m.seen))
	}
}

func TestAskAnswerIsEssentialCompactionAndProgressEvidence(t *testing.T) {
	c := passedCompactionFixture()
	call := session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "question", Name: "ask", Arguments: `{"question":"Which aesthetic?"}`}}}}
	answer := session.Entry{Kind: "answer", Message: llm.Message{Role: "tool", Name: "ask", ToolCallID: "question", Content: "Restrained industrial amber; teal only for interaction."}}
	c.Entries = append(c.Entries, call, answer)
	r := truthRunner(t, &scriptModel{replies: []string{"Converter checks passed; user prefers restrained industrial amber."}}, 32768)
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		var p compactionReview
		if err := json.Unmarshal([]byte(q.Context), &p); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p.Anchors[len(p.Anchors)-2:], []session.Entry{call, answer}) {
			t.Fatal("ask correlation absent from mandatory review")
		}
		return decision.Verdict{Option: q.Options[0], Confidence: .9}, nil
	}
	next, _, ran, err := r.Compact(t.Context(), c)
	if err != nil || !ran {
		t.Fatal("valid compact failed", err)
	}
	r.Continuation = next
	p, err := r.progressEvidence(t.Context(), nil)
	if err != nil || !reflect.DeepEqual(p.AnsweredQuestions, []session.Entry{call, answer}) {
		t.Fatal("answer lost after compact", err)
	}
}

func TestOpaqueCompactionCacheAcrossInvocations(t *testing.T) {
	c := passedCompactionFixture()
	first := truthRunner(t, &scriptModel{replies: []string{"Checkpoint."}}, 32768)
	if _, _, ran, err := first.Compact(t.Context(), c); err == nil || ran {
		t.Fatal("placeholder admitted")
	}
	cache := first.ExportCompactionCache()
	for _, change := range []string{"unchanged", "source", "settings", "owner"} {
		t.Run(change, func(t *testing.T) {
			model := &scriptModel{replies: []string{"Converter check passed; failed exit and user note remain."}}
			next := truthRunner(t, model, 32768)
			next.ImportCompactionCache(cache)
			input := c.Clone()
			switch change {
			case "source":
				input.Add("note", "user", "Keep keyboard access.", false)
			case "settings":
				next.Compaction.Summary++
			case "owner":
				next.Workspace = "different-owner"
			}
			_, _, ran, err := next.Compact(t.Context(), input)
			if change == "unchanged" {
				if err == nil || ran || len(model.seen) != 0 {
					t.Fatal("unchanged retry regenerated", err)
				}
			} else if err != nil || !ran || len(model.seen) != 1 {
				t.Fatalf("changed candidate did not retry: %v", err)
			}
		})
	}
}
