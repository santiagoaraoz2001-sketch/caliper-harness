package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

func TestProgressProjectionUsesLatestWholePacketReasoning(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "unfinished"}[partial], func(t *testing.T) {
			r := &Runner{Prompts: prompts.Builtin()}
			var w watcher
			for _, text := range []string{strings.Repeat("old deliberation ", 600), strings.Repeat("latest reason α ", 600)} {
				w.completedDiscovery(llm.Message{Role: "assistant", Content: "visible answer", ReasoningContent: text}, 5, 4096)
				r.Continuation.Add("reply", "assistant", text, false)
			}
			before, _ := json.Marshal([]any{r.Continuation, w.recentReplies, w.recentDeliberation})
			p, err := r.progressEvidence(t.Context(), &w)
			if err != nil {
				t.Fatal(err)
			}
			latest := p.OmittedDeliberation[len(p.OmittedDeliberation)-1].VerbatimSuffix
			if partial {
				p.UnfinishedResponse = &unfinishedSample{Field: "reasoning", Text: "actual current partial thought", OriginalBytes: 28}
			}
			p.deduplicateEvidence()
			samples, total := 0, 0
			for i, e := range p.OmittedDeliberation {
				if e.VerbatimSuffix != "" {
					samples++
					total += len(e.VerbatimSuffix)
					if i != len(p.OmittedDeliberation)-1 || e.VerbatimSuffix != latest {
						t.Fatal("latest exact sample changed")
					}
				} else if e.OmittedBytes != e.Bytes || e.SHA256 == "" {
					t.Fatal("omitted old sample lost provenance")
				}
			}
			if partial && samples != 0 || !partial && samples != 1 || total > 4096 {
				t.Errorf("sample budget multiplied across history: samples%d bytes%d", samples, total)
			}
			after, _ := json.Marshal([]any{r.Continuation, w.recentReplies, w.recentDeliberation})
			if string(before) != string(after) {
				t.Fatal("judge projection changed raw worker/history evidence")
			}
		})
	}
}

func TestProgressProjectionRetainsVerificationEvidence(t *testing.T) {
	for _, status := range []string{"passed", "failed", "stale", "historical", "incomplete"} {
		t.Run(status, func(t *testing.T) {
			zero := 0
			r := &Runner{Prompts: prompts.Builtin(), Continuation: session.Continuation{Coding: &session.CodingState{
				TaskID: "actual-task", Revision: 3, Receipt: &session.CheckReceipt{Status: status, Text: "complete configured check output", Revision: 2, ExitCode: &zero, Truncated: true,
					Acceptance: &session.CommandReceipt{Status: status, Text: "complete behavioral check output", Truncated: true}},
			}}}
			var w watcher
			w.recentReceipts = []*loopObservation{{Tool: "check", OutputText: "complete failed or partial result", Failed: true, Truncated: true, Error: "actual observed error"}}
			p, err := r.progressEvidence(t.Context(), &w)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal([]any{r.Continuation.Coding, p.RecentReceipts})
			p.deduplicateEvidence()
			after, _ := json.Marshal([]any{p.Coding, p.RecentReceipts})
			owner, _ := json.Marshal([]any{r.Continuation.Coding, w.recentReceipts})
			if string(before) != string(after) || string(before) != string(owner) {
				t.Fatal("current, failed, stale, historical or partial check evidence changed")
			}
		})
	}
}
