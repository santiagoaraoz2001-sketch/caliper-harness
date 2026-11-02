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

// prune replaces only old consumed tool-output bodies with stubs. Reasoning,
// calls, visible text, user history and schemas remain exact. Consumed
// failure/evidence bodies past the same age bound are stubbed in place
// (kind and first lines retained) beyond the latest few of each kind.
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
	size, messages, n, stubbed := 0, 0, 0, 0
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
	if n == 0 && stubbed == 0 {
		return c.Estimate(), ""
	}
	c.Invalidate()
	report := fmt.Sprintf("pruned %d tool output body(ies) older than %d estimated context tokens; protected entries exempt; raw history retained", n, r.Prune.OutputsAgeTokens)
	if stubbed > 0 {
		report += fmt.Sprintf("; stubbed %d old failure/evidence body(ies) past the same age (latest %d failures/%d evidence kept verbatim)", stubbed, pruneKeepLatestFailures, pruneKeepLatestEvidence)
	}
	return c.Estimate(), report
}

// Kept as a caller-compatible no-op: duplicate discovery exchanges, including
// their reasoning and correlation, stay until model-written compaction.
func (r *Runner) maintainDiscovery(context.Context) (bool, error) {
	return false, nil
}
