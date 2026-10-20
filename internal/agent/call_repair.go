package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

type repairCandidate struct {
	Name, Schema  string
	Arguments     tool.Args
	IgnoredFields []string `json:",omitempty"`
}

type callRepairPacket struct {
	OriginalName      string
	AuthoredArguments tool.Args
	Refusal           string
	Candidates        []repairCandidate
	CurrentReply      []llm.Message
	WorkerContext     []llm.Message
	Progress          progressPacket
}

// Capacity reduction changes only this judge projection, never worker context.
// The failed arguments, candidates, user messages and instructions stay intact.
func (p *callRepairPacket) reduceForCapacity() bool {
	best, saving := -1, 0
	var omission progressOmission
	for i := range p.WorkerContext {
		m := &p.WorkerContext[i]
		if m.Role != "assistant" && m.Role != "tool" {
			continue
		}
		raw, err := json.Marshal(m)
		if err != nil {
			continue
		}
		if m.Content == "" && m.Reasoning == "" && m.ReasoningContent == "" && m.Thinking == "" && len(m.ToolCalls) == 0 {
			continue
		}
		stripped, _ := json.Marshal(llm.Message{Role: m.Role, Name: m.Name, ToolCallID: m.ToolCallID})
		note := progressOmission{fmt.Sprintf("WorkerContext[%d]", i), fmt.Sprintf("%x", sha256.Sum256(raw)), len(raw)}
		disclosure, _ := json.Marshal(note)
		if net := len(raw) - len(stripped) - len(disclosure); net > saving {
			best, saving, omission = i, net, note
		}
	}
	// Remove the largest eligible historical body before retrying the server,
	// rather than spending a request on each small early receipt.
	if best >= 0 {
		before, err := json.Marshal(p)
		if err != nil {
			return false
		}
		m := &p.WorkerContext[best]
		original := m.Clone()
		*m = llm.Message{Role: original.Role, Name: original.Name, ToolCallID: original.ToolCallID}
		p.Progress.CapacityOmissions = append(p.Progress.CapacityOmissions, omission)
		after, err := json.Marshal(p)
		if err == nil && len(after) < len(before) {
			return true
		}
		*m = original
		p.Progress.CapacityOmissions = p.Progress.CapacityOmissions[:len(p.Progress.CapacityOmissions)-1]
	}
	return p.Progress.reduceForCapacity()
}

// repairInvocation selects only prevalidated reconstructions of the current
// authored argument object. It neither authors missing values nor runs a tool.
// Callers still own binding, whole-array preflight, authorization and execution.
func (r *Runner) repairInvocation(ctx context.Context, originalName string, authored tool.Args, refusal string, log func(string, string) error, current ...llm.Message) (tool.Definition, tool.Args, bool, error) {
	unchanged := func(err error) (tool.Definition, tool.Args, bool, error) { return tool.Definition{}, nil, false, err }
	if err := ctx.Err(); err != nil {
		return unchanged(err)
	}
	if r.Judge == nil || r.Registry == nil {
		return unchanged(nil)
	}
	if authored == nil {
		authored = tool.Args{}
	}
	var candidates []repairCandidate
	var definitions []tool.Definition
	// An identifiable tool with missing answers is not an invitation to select
	// a different operation merely because that operation accepts empty input.
	intended, identified := r.Registry.ResolveName(originalName)
	for _, name := range r.Registry.Names() {
		def, ok := r.Registry.Lookup(name)
		if !ok || identified && def.Name != intended.Name {
			continue
		}
		input, ignored := tool.NormalizeOpening(def, authored)
		// A different opener must not swallow data meant for another tool.
		if len(ignored) > 0 {
			original, known := r.Registry.ResolveName(originalName)
			if !known || original.Name != def.Name {
				continue
			}
		}
		preserved := true
		for key := range input {
			canonical, supported := tool.ArgumentName(def, key), false
			for _, param := range def.Params {
				supported = supported || canonical == param.Name
			}
			preserved = preserved && supported
		}
		if !preserved {
			continue
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return unchanged(err)
		}
		args, q := tool.ValidateJSON(def, string(raw))
		if q != "" {
			continue
		}
		candidates = append(candidates, repairCandidate{def.Name, def.Schema(), args, ignored})
		definitions = append(definitions, def)
	}
	if len(candidates) == 0 {
		return unchanged(nil)
	}
	progress, err := r.progressEvidence(ctx, nil)
	if err != nil {
		return unchanged(err)
	}
	packet := callRepairPacket{OriginalName: originalName, AuthoredArguments: authored, Refusal: refusal, Candidates: candidates, Progress: progress}
	for _, message := range current {
		packet.CurrentReply = append(packet.CurrentReply, message.Clone())
	}
	for _, entry := range r.Continuation.Entries {
		packet.WorkerContext = append(packet.WorkerContext, entry.Message.Clone())
	}
	options := []string{r.Prompts.Text("decision.call_repair.decline")}
	for i, candidate := range candidates {
		options = append(options, fmt.Sprintf("Use candidate %d: %s", i+1, candidate.Name))
	}
	req, verdict, err := r.progressJudgment(ctx, &packet, decision.Request{Instructions: r.Prompts.Text("decision.call_repair.instructions"), Options: options}, packet.reduceForCapacity, log)
	if err != nil {
		if ctx.Err() != nil {
			return unchanged(ctx.Err())
		}
		var unavailable *progressJudgeError
		if !errors.As(err, &unavailable) {
			return unchanged(err)
		}
		return unchanged(log("judgment-error", "source=tool-call-repair error="+err.Error()))
	}
	if err := ctx.Err(); err != nil {
		return unchanged(err)
	}
	index := verdict.Pick
	valid := index >= 0 && index < len(options) && verdict.Option == options[index] && !math.IsNaN(verdict.Confidence) && !math.IsInf(verdict.Confidence, 0) && verdict.Confidence >= 0 && verdict.Confidence <= 1
	resolution := "unrepaired"
	selected := valid && index > 0 && verdict.Confidence >= advisoryConfidence
	if selected {
		resolution = "candidate-selected; execution pending normal preflight and authorization"
	}
	var saved error
	decision.LogJudgment(func(kind, text string) {
		if saved == nil {
			saved = log(kind, text)
		}
	}, "tool-call-repair", req, verdict, resolution, r.JudgmentContextChars)
	if saved != nil {
		return unchanged(saved)
	}
	if !selected {
		return unchanged(nil)
	}
	candidate := candidates[index-1]
	raw, err := json.Marshal(struct {
		OriginalName      string
		AuthoredArguments tool.Args
		Selected          repairCandidate
	}{originalName, authored, candidate})
	if err == nil {
		err = log("tool-repair", string(raw))
	}
	if err != nil {
		return unchanged(err)
	}
	return definitions[index-1], candidate.Arguments, true, nil
}
