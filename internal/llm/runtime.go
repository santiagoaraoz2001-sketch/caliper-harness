package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	RuntimeOpenAI = "openai"
	RuntimeOllama = "ollama"
	RuntimeMLX    = "mlx"
	// RuntimeRulith is an identity, not a dialect: inference uses the
	// generic OpenAI wire; the name preserves which server family owns
	// lifecycle (the Rulith manager) in evidence and selection.
	RuntimeRulith = "rulith"
)

// WithRuntime chooses an explicit wire contract. Empty means generic OpenAI.
// Construct a new client on endpoint/model selection; observations belong to it.
func (c *Client) WithRuntime(runtime string) *Client { c.runtime = runtime; return c }
func (c *Client) runtimeError() error {
	if c.MaxOutputTokens != nil && *c.MaxOutputTokens < 0 {
		return fmt.Errorf("params.max_output_tokens cannot be negative, got %d", *c.MaxOutputTokens)
	}
	switch c.runtime {
	case "", RuntimeOpenAI, RuntimeOllama, RuntimeMLX, RuntimeRulith:
		return nil
	}
	return fmt.Errorf("model runtime %q is unsupported; choose openai, ollama, mlx, or rulith", c.runtime)
}

// ParameterStatus describes a requested mapping, not sampler acknowledgement.
// Unknown controls never imply an effective ON/OFF state.
type ParameterStatus struct {
	Name      string
	Requested any
	Support   string
	Reason    string
}

func (c *Client) ParameterStatus() []ParameterStatus {
	var out []ParameterStatus
	add := func(name string, value any, support, reason string) {
		out = append(out, ParameterStatus{name, value, support, reason})
	}
	if c.Temperature != nil {
		add("temperature", *c.Temperature, "supported", "documented mapping; model acceptance is server-dependent")
	}
	advancedSupport, advancedReason := "unknown", "not applied: generic compatibility has no standard mapping"
	if c.runtime == RuntimeOllama || c.runtime == RuntimeMLX {
		advancedSupport, advancedReason = "supported", "documented native sampler mapping; neutral values may disable the sampler"
	}
	if c.TopK != nil {
		add("top_k", *c.TopK, advancedSupport, advancedReason)
	}
	if c.RepetitionPenalty != nil {
		add("repetition_penalty", *c.RepetitionPenalty, advancedSupport, advancedReason)
	}
	if c.Thinking != nil {
		support, reason := "unknown", "not applied: no generic boolean thinking contract"
		if c.runtime == RuntimeMLX {
			reason = "template flag sent; selected template support unconfirmed"
		}
		if c.runtime == RuntimeOllama {
			c.thinkingMu.Lock()
			if c.thinkingMeta != nil {
				support, reason = c.thinkingMeta.status(*c.Thinking)
			} else {
				reason = "not applied until selected-model metadata admits this value"
			}
			c.thinkingMu.Unlock()
		}
		add("thinking", *c.Thinking, support, reason)
	}
	if c.MaxOutputTokens != nil {
		support, reason := "unknown", "max_completion_tokens sent; generic server acceptance unconfirmed; includes reasoning"
		if c.runtime == RuntimeMLX || c.runtime == RuntimeOllama {
			support, reason = "supported", "documented native generation ceiling including reasoning; server acceptance is not an acknowledgement"
		}
		if *c.MaxOutputTokens == 0 {
			support, reason = "unknown", "omitted: runtime generation policy unknown, not unlimited"
		}
		add("max_output_tokens", *c.MaxOutputTokens, support, reason)
	}
	if err := c.runtimeError(); err != nil {
		for i := range out {
			out[i].Support = "unsupported"
			out[i].Reason = "not applied: " + err.Error()
		}
	}
	return out
}

// ControlNotice discloses unconfirmed/unsupported requested controls once at
// construction/run boundaries. It never describes requested thinking as effective.
func (c *Client) ControlNotice() string {
	var parts []string
	for _, p := range c.ParameterStatus() {
		if p.Support != "supported" {
			parts = append(parts, fmt.Sprintf("%s %v requested · %s (%s)", p.Name, p.Requested, p.Support, p.Reason))
		}
	}
	return strings.Join(parts, "; ")
}

// RequestedControlNotice describes configuration before a client has model metadata.
func RequestedControlNotice(runtime string, params Params) string {
	return (&Client{runtime: runtime}).WithParams(params).ControlNotice()
}

type thinkingMetadata struct {
	tools           *bool
	allowed         map[bool]bool
	support, reason string
}

func (m *thinkingMetadata) status(value bool) (string, string) {
	if m.support != "supported" {
		return m.support, m.reason
	}
	if !m.allowed[value] {
		return "unsupported", "not applied: selected model does not admit this boolean value"
	}
	return "supported", "selected-model /api/show admits this boolean; not a per-request acknowledgement"
}

// Native model metadata is read once per immutable endpoint/model client.
// Failed/ambiguous metadata remains unknown and the boolean is omitted.
func (c *Client) ollamaThinking(ctx context.Context) *bool {
	if c.Thinking == nil {
		return nil
	}
	m := c.modelMetadata(ctx)
	if m == nil {
		return nil
	}
	support, _ := m.status(*c.Thinking)
	if support == "supported" {
		return c.Thinking
	}
	return nil
}
func (c *Client) modelMetadata(ctx context.Context) *thinkingMetadata {
	c.thinkingMu.Lock()
	if c.thinkingMeta == nil {
		ready := c.thinkingReady
		if ready == nil {
			ready = make(chan struct{})
			c.thinkingReady = ready
			c.thinkingMu.Unlock()
			meta := c.readThinkingMetadata(ctx)
			c.thinkingMu.Lock()
			c.thinkingMeta = meta
			close(ready)
		} else {
			c.thinkingMu.Unlock()
			select {
			case <-ctx.Done():
				return nil
			case <-ready:
			}
			c.thinkingMu.Lock()
		}
	}
	m := c.thinkingMeta
	c.thinkingMu.Unlock()
	return m
}
func (c *Client) readThinkingMetadata(ctx context.Context) (metadata *thinkingMetadata) {
	var tools *bool
	defer func() {
		if metadata != nil {
			metadata.tools = tools
		}
	}()
	unknown := func(reason string) *thinkingMetadata {
		return &thinkingMetadata{support: "unknown", reason: "not applied: " + reason}
	}
	body, _ := json.Marshal(map[string]string{"model": c.model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ollamaRoot()+"/api/show", bytes.NewReader(body))
	if err != nil {
		return unknown(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, nil)
	if err != nil {
		return unknown("selected-model metadata unavailable: " + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil || len(raw) > 32<<20 {
		return unknown("selected-model metadata incomplete")
	}
	if resp.StatusCode != http.StatusOK {
		return unknown(fmt.Sprintf("selected-model metadata HTTP %d", resp.StatusCode))
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
		Thinking     *struct {
			Values []json.RawMessage `json:"values"`
		} `json:"thinking"`
		Error json.RawMessage `json:"error"`
	}
	if err := decodeSingle(raw, &show); err != nil {
		return unknown("invalid selected-model metadata: " + err.Error())
	}
	if _, refused := errorMessage(show.Error); refused {
		return unknown("selected-model metadata refused")
	}
	if show.Capabilities != nil {
		admitted := false
		for _, cap := range show.Capabilities {
			admitted = admitted || cap == "tools"
		}
		tools = &admitted
	}
	if show.Thinking != nil && len(show.Thinking.Values) > 0 {
		m := &thinkingMetadata{support: "supported", allowed: map[bool]bool{}}
		for _, raw := range show.Thinking.Values {
			if string(raw) == "true" || string(raw) == "false" {
				m.allowed[string(raw) == "true"] = true
			}
		}
		return m
	}
	for _, cap := range show.Capabilities {
		if cap == "thinking" {
			return unknown("model advertises thinking without admitted boolean values")
		}
	}
	if show.Capabilities == nil {
		return unknown("metadata omits capabilities")
	}
	return &thinkingMetadata{support: "unsupported", reason: "not applied: selected model has no thinking capability"}
}
func decodeSingle(raw []byte, value any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected extra JSON")
		}
		return err
	}
	return nil
}
