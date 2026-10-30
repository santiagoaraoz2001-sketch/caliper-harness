package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Rulith's chat stream omits the raw tool framing and can omit generated token
// pieces. Its existing native completion stream retains literal UTF-8 output.
// Render once through the server's own template, then continue that exact byte
// prefix. No assistant/user instruction is inserted. Tokenization is performed
// again by the server; this contract does not claim original-token replay.
func (c *Client) chatRulithRaw(ctx context.Context, msgs []Message, tools []ToolDefinition, onDelta func(string)) (reply Reply, err error) {
	reply.Message.Role = "assistant"
	if err := c.runtimeError(); err != nil {
		return reply, err
	}
	render := c.request(msgs, false)
	render.Tools = tools
	identity, err := json.Marshal(render)
	if err != nil {
		return reply, fmt.Errorf("encode Rulith original request: %w", err)
	}
	requestHash := byteHash(identity)
	// Identity includes the full logical schema and placement. Filtering before
	// hashing would let changed hidden fields or placement reuse a stale prefix.
	for i, def := range tools {
		if def.Function.Name == "plan" && def.TailPublished && len(tools) > 1 {
			identity, err = json.Marshal(struct {
				Request   chatRequest
				Placement string
			}{render, "host-plan-tail-v1"})
			if err != nil {
				return reply, err
			}
			requestHash = byteHash(identity)
			render.Tools = append(append([]ToolDefinition(nil), tools[:i]...), tools[i+1:]...)
			break
		}
	}
	previous, continuing := ctx.Value(responseContinuationKey{}).(Reply)
	var prefix *ResponsePrefix
	if continuing {
		prefix = previous.Prefix.Clone()
		if prefix == nil || prefix.Strategy != RulithRawPrefix || prefix.Validate() != nil || prefix.RequestSHA256 != requestHash || previous.RawNative != prefix.Raw {
			return reply, fmt.Errorf("Rulith continuation does not match the original request and raw response")
		}
		reply.RawNative, reply.NativeInitialState, reply.Served = prefix.Raw, prefix.InitialState, previous.Served
		reply.RawContentStreamed = previous.RawContentStreamed
	} else {
		rendered, err := c.rulithRenderedPrompt(ctx, render)
		if err != nil {
			return reply, err
		}
		if rendered == "" || !utf8.ValidString(rendered) {
			return reply, fmt.Errorf("Rulith template returned no valid rendered prompt")
		}
		state := "normal"
		if strings.HasSuffix(strings.TrimRight(rendered, " \t\r\n"), "<think>") {
			state = "reasoning"
		}
		prefix = &ResponsePrefix{Strategy: RulithRawPrefix, Prompt: rendered, PromptSHA256: byteHash([]byte(rendered)), RequestSHA256: requestHash, InitialState: state}
		reply.NativeInitialState = state
	}
	// A native string prompt preserves the exact rendered bytes. In particular,
	// do not reserialize a partially decoded tool call or trim its trailing bytes.
	prompt := prefix.Prompt + prefix.Raw
	if len(prompt) > 32<<20 {
		return reply, fmt.Errorf("Rulith rendered prefix exceeds transport limit")
	}
	body := map[string]any{"prompt": prompt, "stream": true, "cache_prompt": true, "n_keep": -1}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.MaxOutputTokens != nil && *c.MaxOutputTokens > 0 {
		body["n_predict"] = *c.MaxOutputTokens
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return reply, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rulithRoot()+"/completion", bytes.NewReader(encoded))
	if err != nil {
		return reply, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	activity := c.streamActivity()
	defer activity("")
	resp, err := c.do(req, &reply)
	if err != nil {
		return reply, c.plainConnErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return reply, fmt.Errorf("Rulith native completion HTTP %d: %s", resp.StatusCode, raw)
	}
	var terminal *rulithChunk
	var output strings.Builder
	output.WriteString(prefix.Raw)
	defer func() { reply.RawNative = output.String() }()
	projection := rulithProjection{reasoning: reply.NativeInitialState == "reasoning", reasoned: reply.NativeInitialState == "reasoning"}
	// Restore framing only. Retained bytes are never replayed to observers.
	_ = projection.feed(prefix.Raw, nil)
	display := onDelta != nil && projection.prose.String() == reply.RawContentStreamed
	deliver := func(field, delta string) error {
		if err := observeAux(ctx, field, delta); err != nil {
			return err
		}
		if field == "content" && display {
			onDelta(delta)
			reply.RawContentStreamed += delta
		}
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			return reply, fmt.Errorf("Rulith stream contains a non-SSE record; response is partial")
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if terminal == nil {
				return reply, fmt.Errorf("Rulith stream ended without a terminal receipt")
			}
			continue
		}
		if terminal != nil {
			return reply, fmt.Errorf("Rulith stream contains data after terminal receipt")
		}
		var chunk rulithChunk
		if !utf8.ValidString(data) || decodeSingle([]byte(data), &chunk) != nil {
			return reply, fmt.Errorf("Rulith stream contains an invalid JSON record; response is partial")
		}
		if message, refused := errorMessage(chunk.Error); refused {
			return reply, fmt.Errorf("Rulith native completion refused: %s", message)
		}
		if chunk.Index != 0 {
			return reply, fmt.Errorf("Rulith stream changed response index")
		}
		if chunk.Model != "" {
			if reply.Served != "" && reply.Served != chunk.Model {
				return reply, fmt.Errorf("Rulith stream changed serving model")
			}
			reply.Served = chunk.Model
		}
		if chunk.Content != "" {
			if !utf8.ValidString(chunk.Content) {
				return reply, fmt.Errorf("Rulith stream returned invalid UTF-8 content")
			}
			output.WriteString(chunk.Content)
			if output.Len() > 32<<20 {
				return reply, fmt.Errorf("Rulith response exceeds transport limit")
			}
			activity("generating")
			if err := projection.feed(chunk.Content, deliver); err != nil {
				return reply, err
			}
			// Native frames are not public assistant prose. The owning registry
			// parses the full response before publishing content or dispatching calls.
		} else {
			activity("active")
		}
		if chunk.Stop {
			terminal = &chunk
			if chunk.Model == "" {
				return reply, fmt.Errorf("Rulith terminal omitted serving model identity")
			}
			if chunk.Prompt == nil || *chunk.Prompt != prompt {
				return reply, fmt.Errorf("Rulith terminal prompt differs from the exact submitted prefix")
			}
			if chunk.Truncated {
				return reply, fmt.Errorf("Rulith shifted or truncated the context; exact byte-prefix continuation refused")
			}
			if chunk.TokensEvaluated == nil || chunk.TokensPredicted == nil || *chunk.TokensEvaluated < 0 || *chunk.TokensPredicted < 0 {
				return reply, fmt.Errorf("Rulith terminal omitted valid usage counts")
			}
			reply.Usage = Usage{In: *chunk.TokensEvaluated, Out: *chunk.TokensPredicted, Reported: true}
		}
	}
	if err := scanner.Err(); err != nil {
		return reply, fmt.Errorf("Rulith native stream broke: %w; response is partial", err)
	}
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	if terminal == nil {
		return reply, fmt.Errorf("Rulith native stream omitted a terminal receipt; response is partial")
	}
	switch terminal.StopType {
	case "limit":
		if output.Len() <= len(prefix.Raw) || reply.Usage.Out == 0 {
			return reply, fmt.Errorf("Rulith output limit made no prefix progress")
		}
		prefix.Raw = output.String()
		prefix.GeneratedTokens += reply.Usage.Out
		if err := prefix.Validate(); err != nil {
			return reply, err
		}
		if continuing && !extendsPrefix(previous.Prefix, prefix) {
			return reply, fmt.Errorf("Rulith unfinished response did not extend the same literal byte prefix")
		}
		reply.Prefix, reply.OutputLimit, reply.FinishReason = prefix, true, "length"
		return reply, c.finishError("length")
	case "eos":
		if err := projection.finish(deliver); err != nil {
			return reply, err
		}
		reply.FinishReason = "stop"
		return reply, nil
	case "word":
		// No custom stop word was requested. Treat a server stop policy as
		// incomplete unless the parser/host has an explicit matching contract.
		return reply, fmt.Errorf("Rulith stopped at an unrequested stop word; response is partial")
	default:
		return reply, fmt.Errorf("Rulith returned unknown terminal stop_type %q", terminal.StopType)
	}
}

// rulithProjection observes only confirmed leading reasoning and prose. It
// stops permanently at any tool/control frame; the registry alone parses calls.
// A possible marker suffix is held until the following bytes disambiguate it.
type rulithProjection struct {
	pending                                     string
	reasoning, reasoned, stopped, nonWhitespace bool
	prose                                       strings.Builder
}

func (p *rulithProjection) emit(field, text string, send func(string, string) error) error {
	if text == "" {
		return nil
	}
	if field == "content" {
		p.prose.WriteString(text)
		p.nonWhitespace = p.nonWhitespace || strings.TrimSpace(text) != ""
	}
	if send != nil {
		return send(field, text)
	}
	return nil
}

func (p *rulithProjection) feed(text string, send func(string, string) error) error {
	if p.stopped {
		return nil
	}
	p.pending += text
	for p.pending != "" {
		if p.reasoning {
			const close = "</think>"
			if at := strings.Index(p.pending, close); at >= 0 {
				if err := p.emit("reasoning_content", p.pending[:at], send); err != nil {
					return err
				}
				p.pending = p.pending[at+len(close):]
				p.reasoning = false
				continue
			}
			keep := min(len(p.pending), len(close)-1)
			for keep > 0 && !strings.HasPrefix(close, p.pending[len(p.pending)-keep:]) {
				keep--
			}
			at := len(p.pending) - keep
			if err := p.emit("reasoning_content", p.pending[:at], send); err != nil {
				return err
			}
			p.pending = p.pending[at:]
			return nil
		}
		at := strings.IndexByte(p.pending, '<')
		if at < 0 {
			text := p.pending
			p.pending = ""
			return p.emit("content", text, send)
		}
		if at > 0 {
			if err := p.emit("content", p.pending[:at], send); err != nil {
				return err
			}
			p.pending = p.pending[at:]
		}
		const open = "<think>"
		if strings.HasPrefix(p.pending, open) {
			if p.reasoned || p.nonWhitespace {
				p.stopped, p.pending = true, ""
				return nil
			}
			p.reasoning, p.reasoned = true, true
			p.pending = p.pending[len(open):]
			continue
		}
		if strings.HasPrefix(open, p.pending) {
			return nil
		}
		for _, marker := range []string{"<tool_call", "</tool_call", "</think", "<function=", "</function>", "<parameter=", "</parameter>"} {
			if strings.HasPrefix(p.pending, marker) {
				p.stopped, p.pending = true, ""
				return nil
			}
			if strings.HasPrefix(marker, p.pending) {
				return nil
			}
		}
		// This '<' cannot begin a native boundary (for example '<b>' or '< 3').
		if err := p.emit("content", "<", send); err != nil {
			return err
		}
		p.pending = p.pending[1:]
	}
	return nil
}

func (p *rulithProjection) finish(send func(string, string) error) error {
	if p.stopped || p.reasoning || p.pending == "" {
		return nil
	}
	// At a clean stop an unfinished prefix such as '<' is literal prose;
	// recognized native boundaries are left for the strict parser to reject.
	if strings.HasPrefix(p.pending, "<think") {
		return nil
	}
	text := p.pending
	p.pending = ""
	return p.emit("content", text, send)
}

type rulithChunk struct {
	Content         string          `json:"content"`
	Index           int             `json:"index"`
	Model           string          `json:"model"`
	Stop            bool            `json:"stop"`
	StopType        string          `json:"stop_type"`
	Prompt          *string         `json:"prompt"`
	Truncated       bool            `json:"truncated"`
	TokensEvaluated *int            `json:"tokens_evaluated"`
	TokensPredicted *int            `json:"tokens_predicted"`
	Error           json.RawMessage `json:"error"`
}

func byteHash(b []byte) string       { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (c *Client) rulithRoot() string { return strings.TrimSuffix(c.baseURL, "/v1") }

func (c *Client) rulithJSON(ctx context.Context, path string, body, result any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rulithRoot()+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, nil)
	if err != nil {
		return c.plainConnErr(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 32<<20 {
		return fmt.Errorf("Rulith template response exceeds transport limit")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Rulith %s HTTP %d: %.4096s", path, resp.StatusCode, raw)
	}
	if err := decodeSingle(raw, result); err != nil {
		return fmt.Errorf("Rulith %s invalid response: %w", path, err)
	}
	return nil
}
