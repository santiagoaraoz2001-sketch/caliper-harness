package agent

import (
	"fmt"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Old failure/evidence bodies must not accumulate verbatim forever: prune
// stubs consumed bodies past the age bound, keeping the latest entries of
// each kind exact. Kinds survive so anchors still classify them.
func TestPruneStubsOldFailuresAndEvidence(t *testing.T) {
	r := pruneRunner()
	c := session.Continuation{Version: 1}
	c.Entries = append(c.Entries,
		session.Entry{Kind: "injected", Message: llm.Message{Role: "system", Content: "sys"}},
		session.Entry{Kind: "intent", Message: llm.Message{Role: "user", Content: "go"}},
	)
	for i := 0; i < 12; i++ {
		c.Entries = append(c.Entries, session.Entry{Kind: "failure", Message: llm.Message{Role: "user", Content: fmt.Sprintf("tool result:\nboom %d\n%s", i, strings.Repeat("f", 2000))}})
		c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: "ok"}})
	}
	for i := 0; i < 6; i++ {
		c.Entries = append(c.Entries, session.Entry{Kind: "evidence", Message: llm.Message{Role: "user", Content: fmt.Sprintf("host receipt %d\n%s", i, strings.Repeat("e", 3000))}})
		c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: "ok"}})
	}
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Content: strings.Repeat("r", 60000)}})
	msgs := c.Messages()
	c.BasisTokens = contentSize(msgs)
	c.BasisChars = c.BasisTokens

	_, report := r.prune(&c)
	if report == "" {
		t.Fatal("expected pruning report")
	}
	var failures, evidences []session.Entry
	for _, e := range c.Entries {
		switch e.Kind {
		case "failure":
			failures = append(failures, e)
		case "evidence":
			evidences = append(evidences, e)
		}
	}
	if len(failures) != 12 || len(evidences) != 6 {
		t.Fatalf("entries must be retained as stubs, failures=%d evidence=%d", len(failures), len(evidences))
	}
	stubbedFailures := 0
	for i, e := range failures {
		stub := strings.Contains(e.Message.Content, "pruned")
		if stub {
			stubbedFailures++
			if !strings.HasPrefix(e.Message.Content, "tool result:\n") || !strings.Contains(e.Message.Content, fmt.Sprintf("boom %d", i)) {
				t.Errorf("failure stub %d lost its first line: %q", i, e.Message.Content[:min(80, len(e.Message.Content))])
			}
		}
	}
	if stubbedFailures == 0 {
		t.Fatal("old failure bodies were not stubbed")
	}
	// The latest failures stay verbatim.
	if strings.Contains(failures[len(failures)-1].Message.Content, "pruned") {
		t.Fatal("latest failure must stay verbatim")
	}
	stubbedEvidence := 0
	for _, e := range evidences {
		if strings.Contains(e.Message.Content, "pruned") {
			stubbedEvidence++
		}
	}
	if stubbedEvidence == 0 {
		t.Fatal("old evidence bodies were not stubbed")
	}
	if strings.Contains(evidences[len(evidences)-1].Message.Content, "pruned") {
		t.Fatal("latest evidence must stay verbatim")
	}
}
