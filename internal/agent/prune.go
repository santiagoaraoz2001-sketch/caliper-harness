package agent

import (
	"context"
	"fmt"
	"math"
	"specific-harness/internal/session"
)

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

// prune replaces only old consumed tool-output bodies. Calls, reasoning, user
// history and schemas remain exact. Age counts all later message context,
// including reasoning and later schema messages, using admission's calibration.
// Leading tool definitions contribute to occupancy, not to an output's age.
// Original sizes govern this pass, so one replacement cannot age another result.
func (r *Runner) prune(c *session.Continuation) (int, string) {
	if r.Prune.OutputsAgeTokens <= 0 {
		return c.Estimate(), ""
	}
	keep := c.Protected(r.Prune.SchemasKeep)
	size, messages, n := 0, 0, 0
	for i := len(c.Entries) - 1; i >= 0; i-- {
		age := size + 20*messages // Continuation's disclosed fallback estimate.
		if c.BasisChars > 0 && c.BasisTokens > 0 {
			age = int(math.Ceil(float64(size) * float64(c.BasisTokens) / float64(c.BasisChars)))
		}
		e := &c.Entries[i]
		size += e.Message.Size()
		messages++
		if e.Kind != "output" || e.Message.Role == "system" || keep[i] || age <= r.Prune.OutputsAgeTokens {
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
	if n == 0 {
		return c.Estimate(), ""
	}
	c.Invalidate()
	return c.Estimate(), fmt.Sprintf("pruned %d tool output(s) older than %d estimated context tokens; protected entries exempt; raw history retained", n, r.Prune.OutputsAgeTokens)
}

// Kept as a caller-compatible no-op: duplicate discovery exchanges, including
// their reasoning and correlation, stay until model-written compaction.
func (r *Runner) maintainDiscovery(context.Context) (bool, error) {
	return false, nil
}
