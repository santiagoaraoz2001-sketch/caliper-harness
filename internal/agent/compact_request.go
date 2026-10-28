package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"specific-harness/internal/decision"
	"specific-harness/internal/session"
)

type compactionCandidate struct {
	Key       [32]byte
	Summary   string
	Rejected  bool   // A settled rejected candidate cannot gain authority by being judged repeatedly.
	Rejection string // Same source/config must not pay for the same unusable candidate again.
}

// CompactionCache carries at most one immutable candidate between invocations
// by the same UI owner. It contains no runner, model, store or live resource.
// Import never admits it: Compact still checks its exact source/settings key.
type CompactionCache struct{ candidate *compactionCandidate }

func (r *Runner) ExportCompactionCache() CompactionCache {
	if r.compactionCandidate == nil {
		return CompactionCache{}
	}
	copy := *r.compactionCandidate
	return CompactionCache{candidate: &copy}
}

func (r *Runner) ImportCompactionCache(cache CompactionCache) {
	r.compactionCandidate = nil
	if cache.candidate != nil {
		copy := *cache.candidate
		r.compactionCandidate = &copy
	}
}

type compactionProjection struct {
	// These records are NOT deleted or summarized. They remain verbatim in
	// the continuation. The judge must not claim to have checked omitted facts.
	Notice           string
	Anchors, CallIDs int
	SHA256           string
}

type compactionReview struct {
	Summary               string
	Anchors               []session.Entry
	Coding                *session.CodingState
	Assignment            *session.Assignment
	Workflow              *session.WorkflowScope
	SourceSHA256          string
	SourceBytes           int
	SourceCallIDs         []string
	SessionID             string
	RepeatedAnchorContent []anchorContentReference `json:",omitempty"`
	RetainedOutsideReview *compactionProjection    `json:",omitempty"`
}

const compactionAccept = "the summary is factually consistent with essential anchors"

// compactionRequest retains all user instructions, unread anchors, latest
// observations/advice and owning coding/workflow state. Only older host records
// which Compact already retains verbatim may leave the transport projection.
// Small requests keep their complete, lossless historical anchor representation.
func (r *Runner) compactionRequest(c session.Continuation, source, summary string) (decision.Request, error) {
	return r.compactionRequestLimit(c, source, summary, 0)
}

func (r *Runner) compactionRequestLimit(c session.Continuation, source, summary string, maxContextBytes int) (decision.Request, error) {
	p := compactionReview{Summary: summary, Coding: c.Coding, Assignment: c.Assignment, Workflow: c.Workflow,
		SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), SourceBytes: len(source), SessionID: r.SessionID,
		Anchors: []session.Entry{}, SourceCallIDs: []string{}}
	latest := map[string]int{}
	essentialAnswer := map[int]bool{}
	answers := answerGroupEntries(c)
	for i, e := range c.Entries {
		anchor := answers[i]
		switch e.Kind {
		case "intent", "task", "note", "answer", "failure", "evidence", "guidance":
			anchor = true
		}
		if anchor {
			essentialAnswer[len(p.Anchors)] = answers[i]
			latest[e.Kind] = len(p.Anchors)
			p.Anchors = append(p.Anchors, e)
		}
		for _, call := range e.Message.ToolCalls {
			p.SourceCallIDs = append(p.SourceCallIDs, call.ID)
		}
	}
	anchors := p.Anchors
	removed := make([]bool, len(anchors))
	var omitted []session.Entry
	var omittedIDs []string
	for {
		p.Anchors = nil
		for i, e := range anchors {
			if !removed[i] {
				p.Anchors = append(p.Anchors, e)
			}
		}
		p.Anchors, p.RepeatedAnchorContent = shareAnchorContent(p.Anchors)
		if len(omitted)+len(omittedIDs) > 0 {
			raw, err := json.Marshal(struct {
				Anchors []session.Entry
				CallIDs []string
			}{omitted, omittedIDs})
			if err != nil {
				return decision.Request{}, err
			}
			p.RetainedOutsideReview = &compactionProjection{Notice: "Older protected records remain verbatim in the continuation; historical call IDs remain in the saved source. Their bodies are outside this factual review, not verified or replaced by this summary.", Anchors: len(omitted), CallIDs: len(omittedIDs), SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return decision.Request{}, err
		}
		req := decision.Request{Context: string(raw), Instructions: r.Prompts.Text("decision.compaction.instructions"), Options: []string{compactionAccept, "the summary contradicts, invents or omits essential facts"}}
		if maxContextBytes > 0 && len(raw) > maxContextBytes {
			err = &decision.ContextCapacityError{Message: "native refusal requires a smaller optional projection"}
		} else if r.JudgePreflight != nil {
			err = r.JudgePreflight(req)
		} else {
			err = nil // Injected judges may have no locally known transport ceiling.
		}
		if err == nil {
			return req, nil
		}
		var capacity *decision.ContextCapacityError
		if !errors.As(err, &capacity) {
			return req, err
		}
		// Call IDs are source provenance, not authority or observed outcomes.
		// Their complete sequence is fingerprinted instead of repeatedly sent.
		if len(p.SourceCallIDs) > 0 {
			omittedIDs = p.SourceCallIDs
			p.SourceCallIDs = nil
			continue
		}
		var optional []int
		for i, e := range anchors {
			if removed[i] || essentialAnswer[i] || e.Unread || latest[e.Kind] == i || e.Message.Role == "system" {
				continue
			}
			if e.Kind != "failure" && e.Kind != "evidence" && e.Kind != "guidance" {
				continue
			}
			optional = append(optional, i)
		}
		if len(optional) == 0 {
			return req, fmt.Errorf("compaction protected review exceeds decision capacity; context intact: %w", err)
		}
		// Halve optional history per dry retry rather than marshal once per
		// historical entry. The number of transport checks grows logarithmically.
		for _, i := range optional[:(len(optional)+1)/2] {
			removed[i] = true
			omitted = append(omitted, anchors[i])
		}
	}
}

// An Ask result carries human input inside a tool envelope. Keep its whole
// actual correlation group; neither the result nor the question alone is enough.
func answerGroupEntries(c session.Continuation) []bool {
	keep := make([]bool, len(c.Entries))
	for i := 0; i < len(c.Entries); {
		end := c.GroupEnd(i)
		answer := false
		for j := i; j < end; j++ {
			e := c.Entries[j]
			answer = answer || e.Kind == "answer" || e.Message.Role == "tool" && e.Message.Name == "ask"
		}
		if answer {
			for j := i; j < end; j++ {
				keep[j] = true
			}
		}
		i = end
	}
	return keep
}
