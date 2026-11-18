package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"

	"specific-harness/internal/session"
)

// pruneBatch keeps ordinary requests append-only. Rewriting an old result
// invalidates every cached token after it, even if the replacement is tiny.
// Batch at an already changed instruction/tool prefix or existing pressure
// boundary. This is request-local scheduling, never restored task authority.
type pruneBatch struct {
	prefix [32]byte
	seen   bool
}

func (b *pruneBatch) apply(r *Runner, c *session.Continuation) (int, string) {
	h := sha256.New()
	fmt.Fprintf(h, "%q%q", c.ModelKey, c.SchemaKey)
	for _, e := range c.Entries {
		if e.Message.Role != "system" {
			break
		}
		fmt.Fprintf(h, "%q", e.Message.Content)
	}
	var prefix [32]byte
	copy(prefix[:], h.Sum(nil))
	boundary := !b.seen || prefix != b.prefix
	b.prefix, b.seen = prefix, true
	basis := c.Estimate()
	occupancy := basis
	if c.Measured && c.Prompt > occupancy {
		occupancy = c.Prompt
	}
	if boundary || shouldCompact(occupancy, r.MaxTokens, true, r.Compaction) || overBudget(occupancy, r.MaxTokens, true) {
		return r.prune(c)
	}
	return basis, ""
}

type Prune struct {
	// OutputsAgeTokens is the age of a consumed output body, measured from
	// subsequent context using Continuation's calibrated estimate. Zero disables.
	OutputsAgeTokens int `yaml:"outputs_age_tokens"`
	// Retained for Go caller compatibility only; percentages do not prune.
	OutputsPct float64
	SchemasPct float64
	// Recent schema protection belongs to model-written compaction/resume.
	SchemasKeep int
}

// Latest failure/evidence bodies stay verbatim at any age; older ones are
// stubbed like outputs so protected kinds cannot grow without bound and
// defeat compaction. Counts mirror the latest-N schema protection pattern.
const (
	pruneKeepLatestFailures = 8
	pruneKeepLatestEvidence = 4
	pruneMinStubBytes       = 512
)

// prune replaces old consumed bodies with stubs: tool outputs, failure/
// evidence bodies, assistant reasoning and tool-call arguments. Visible
// text, user history, schemas and call correlation (IDs, names) remain
// exact. Consumed failure/evidence bodies past the age bound are stubbed
// in place (kind and first lines retained) beyond the latest few of each
// kind. Full bodies always remain in raw session history.
// Age counts all later message context,
// including reasoning and later schema messages, using admission's calibration.
// Leading tool definitions contribute to occupancy, not to an output's age.
// Original sizes govern this pass, so one replacement cannot age another result.
func (r *Runner) prune(c *session.Continuation) (int, string) {
	if r.Prune.OutputsAgeTokens <= 0 {
		return c.Estimate(), ""
	}
	keep := c.Protected(r.Prune.SchemasKeep)
	latestKept := make([]bool, len(c.Entries))
	failures, evidences := 0, 0
	for i := len(c.Entries) - 1; i >= 0; i-- {
		switch c.Entries[i].Kind {
		case "failure":
			failures++
			latestKept[i] = failures <= pruneKeepLatestFailures
		case "evidence":
			evidences++
			latestKept[i] = evidences <= pruneKeepLatestEvidence
		}
	}
	size, messages, n, stubbed, bodies := 0, 0, 0, 0, 0
	for i := len(c.Entries) - 1; i >= 0; i-- {
		age := size + 20*messages // Continuation's disclosed fallback estimate.
		if c.BasisChars > 0 && c.BasisTokens > 0 {
			age = int(math.Ceil(float64(size) * float64(c.BasisTokens) / float64(c.BasisChars)))
		}
		e := &c.Entries[i]
		size += e.Message.Size()
		messages++
		if age <= r.Prune.OutputsAgeTokens || e.Message.Role == "system" {
			continue
		}
		// Failure/evidence bodies are protected from summary replacement, not
		// from age stubbing: kind, call association and first lines stay exact.
		if (e.Kind == "failure" || e.Kind == "evidence") && !e.Unread && !latestKept[i] {
			old := len(e.Message.Content)
			if old <= pruneMinStubBytes || strings.Contains(e.Message.Content, "[pruned "+e.Kind+" body") {
				continue
			}
			head := e.Message.Content
			if lines := strings.SplitN(head, "\n", 3); len(lines) > 2 {
				head = lines[0] + "\n" + lines[1]
			}
			if len(head) > 240 {
				head = head[:240]
			}
			stub := fmt.Sprintf("%s\n[pruned %s body (%d bytes; age %d estimated context tokens); full result in raw session history]", head, e.Kind, old, age)
			if len(stub) >= old {
				continue
			}
			e.Message.Content = stub
			stubbed++
			continue
		}
		// Old consumed reasoning and tool-call argument bodies are stubbed
		// in place even inside protected groups: the entries, their visible
		// text and their call correlation stay, only bodies no later step
		// can act on give way. Without this, protected groups accumulate
		// reasoning/argument mass compaction can never retire.
		if e.Message.Role == "assistant" && !e.Unread {
			for _, body := range []*string{&e.Message.Reasoning, &e.Message.ReasoningContent, &e.Message.Thinking} {
				old := len(*body)
				if old <= pruneMinStubBytes || strings.HasPrefix(*body, "[pruned reasoning") {
					continue
				}
				stub := fmt.Sprintf("[pruned reasoning (%d bytes; sha256 %x; age %d estimated context tokens); full text in raw session history]", old, sha256.Sum256([]byte(*body)), age)
				if len(stub) >= old {
					continue
				}
				*body = stub
				bodies++
			}
			for j := range e.Message.ToolCalls {
				call := &e.Message.ToolCalls[j]
				old := len(call.Arguments)
				if old <= pruneMinStubBytes || strings.Contains(call.Arguments, `"_pruned"`) {
					continue
				}
				stub := fmt.Sprintf(`{"_pruned":"tool call arguments (%d bytes; sha256 %x; age %d estimated context tokens); full text in raw session history"}`, old, sha256.Sum256([]byte(call.Arguments)), age)
				if len(stub) >= old {
					continue
				}
				call.Arguments = stub
				bodies++
			}
		}
		if keep[i] {
			continue
		}
		if e.Kind != "output" {
			continue
		}
		old := len(e.Message.Content)
		stub := fmt.Sprintf("[pruned tool output (%d bytes; age %d estimated context tokens); full result in raw session history]", old, age)
		if len(stub) >= old {
			continue // A replacement must actually save context.
		}
		e.Message.Content = stub
		e.Kind = "history" // settled stub; never re-prune or lose call association
		n++
	}
	if n == 0 && stubbed == 0 && bodies == 0 {
		return c.Estimate(), ""
	}
	c.Invalidate()
	report := fmt.Sprintf("pruned %d tool output body(ies) older than %d estimated context tokens; protected entries exempt; raw history retained", n, r.Prune.OutputsAgeTokens)
	if stubbed > 0 {
		report += fmt.Sprintf("; stubbed %d old failure/evidence body(ies) past the same age (latest %d failures/%d evidence kept verbatim)", stubbed, pruneKeepLatestFailures, pruneKeepLatestEvidence)
	}
	if bodies > 0 {
		report += fmt.Sprintf("; stubbed %d old reasoning/tool-call argument body(ies) past the same age", bodies)
	}
	return c.Estimate(), report
}

// Kept as a caller-compatible no-op: duplicate discovery exchanges, including
// their reasoning and correlation, stay until model-written compaction.
func (r *Runner) maintainDiscovery(context.Context) (bool, error) {
	return false, nil
}
