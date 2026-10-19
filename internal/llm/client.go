// Package llm provides explicit OpenAI, MLX and native Ollama chat contracts.
// Errors retain partial evidence and never imply a completed answer.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Preset server URLs.
const (
	VLLMServer = "http://127.0.0.1:8000/v1"
	MLXServer  = "http://127.0.0.1:8080/v1"
)

// Message is one chat turn.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Thinking         string     `json:"thinking,omitempty"`
}

// Usage is one exchange's server-reported token counts: In counts the
// prompt tokens the server read, Out the completion tokens it wrote.
// Reported is false when the server omits usage — zero counts with
// Reported false mean unknown, never free.
type Usage struct {
	In       int
	Out      int
	Reported bool
}

// Client uses the selected runtime contract; URLs do not select a dialect.
type Client struct {
	baseURL       string
	model         string
	http          *http.Client
	runtime       string
	toolProtocol  string
	idleTimeout   time.Duration
	thinkingMu    sync.Mutex
	thinkingMeta  *thinkingMetadata
	thinkingReady chan struct{}
	// Sampling knobs (nil = unset, sends nothing).
	Temperature       *float64
	TopK              *int
	RepetitionPenalty *float64
	Thinking          *bool
	MaxOutputTokens   *int
}

// New returns a client for server (e.g. MLXServer) and model ID.
func New(server, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(server, "/"),
		model:   model,
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// WithoutThinking returns a copy of the client that explicitly disables
// thinking on the wire. Maintenance calls (compaction summaries) use it:
// they must produce a clean document, not spend the output budget on
// reasoning. The original client is unchanged.
func (c *Client) WithoutThinking() *Client {
	off := false
	return &Client{
		baseURL: c.baseURL, model: c.model, http: c.http, runtime: c.runtime,
		toolProtocol: c.toolProtocol, idleTimeout: c.idleTimeout,
		Temperature: c.Temperature, TopK: c.TopK,
		RepetitionPenalty: c.RepetitionPenalty, Thinking: &off,
		MaxOutputTokens: c.MaxOutputTokens,
	}
}

// WithPlainTimeout changes HTTP total only; captured/idle-guarded calls bypass it.
func (c *Client) WithPlainTimeout(timeout time.Duration) *Client {
	c.http.Timeout = timeout
	return c
}

type chatRequest struct {
	ResponsePrefix *ResponsePrefix  `json:"response_prefix,omitempty"`
	Tools          []ToolDefinition `json:"tools,omitempty"`
	Model          string           `json:"model"`
	Messages       []Message        `json:"messages"`
	Stream         bool             `json:"stream"`
	StreamOptions  *streamOptions   `json:"stream_options,omitempty"`
	// MLX sampler fields; generic compatibility sends only temperature.
	// Nil params preserve the old bare-bones body exactly.
	Temperature         *float64        `json:"temperature,omitempty"`
	TopK                *int            `json:"top_k,omitempty"`
	RepetitionPenalty   *float64        `json:"repetition_penalty,omitempty"`
	ChatTemplateKwargs  map[string]bool `json:"chat_template_kwargs,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
}

// Params carries the configured sampling knobs onto the wire.
// Plain values: set means send.
type Params struct {
	Temperature       float64
	TopK              int
	RepetitionPenalty float64
	Thinking          bool
	MaxOutputTokens   int
}

// WithParams attaches sampling knobs, chainable after New.
func (c *Client) WithParams(p Params) *Client {
	c.Temperature = &p.Temperature
	c.TopK = &p.TopK
	c.RepetitionPenalty = &p.RepetitionPenalty
	c.Thinking = &p.Thinking
	c.MaxOutputTokens = &p.MaxOutputTokens
	return c
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageWire      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type usageWire struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type streamChunk struct {
	ResponsePrefix *ResponsePrefix `json:"response_prefix,omitempty"`
	Model          string          `json:"model"`
	Choices        []struct {
		Delta struct {
			Content          string      `json:"content"`
			ToolCalls        []callDelta `json:"tool_calls,omitempty"`
			Reasoning        string      `json:"reasoning"`
			ReasoningContent string      `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageWire      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// request builds the wire body: identity, messages, stream flags,
// plus sampling knobs when set.
func (c *Client) request(msgs []Message, stream bool) chatRequest {
	r := chatRequest{Model: c.model, Messages: msgs, Stream: stream}
	if stream {
		r.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	r.Temperature = c.Temperature
	if c.MaxOutputTokens != nil && *c.MaxOutputTokens > 0 {
		r.MaxCompletionTokens = c.MaxOutputTokens
	}
	if c.runtime == RuntimeMLX {
		r.TopK, r.RepetitionPenalty = c.TopK, c.RepetitionPenalty
		if c.Thinking != nil {
			r.ChatTemplateKwargs = map[string]bool{"enable_thinking": *c.Thinking}
		}
	}
	return r
}

// Chat sends messages and returns the reply text, the serving model's
// self-reported identity, and the exchange's server-reported usage.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, string, Usage, error) {
	return c.chatPlain(ctx, msgs, nil, nil)
}
func (c *Client) chatPlain(ctx context.Context, msgs []Message, tools []ToolDefinition, capture *Reply) (reply, servedModel string, usage Usage, err error) {
	defer func() {
		if capture != nil {
			capture.Message.Content = reply
			capture.Served = servedModel
			capture.Usage = usage
		}
	}()
	if err := c.runtimeError(); err != nil {
		return "", "", Usage{}, err
	}
	if c.runtime == RuntimeOllama {
		return c.chatOllama(ctx, msgs, false, nil, tools, capture)
	}
	request := c.request(msgs, false)
	request.Tools = tools
	body, err := json.Marshal(request)
	if err != nil {
		return "", "", Usage{}, fmt.Errorf("cannot encode request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", Usage{}, fmt.Errorf("cannot build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, capture)
	if err != nil {
		return "", "", Usage{}, c.plainConnErr(err)
	}
	defer resp.Body.Close()
	// Keep the existing 32MiB memory guard, but never silently accept a
	// response cut off by it. Oversize replies are failures, not answers.
	const maxReplyBytes = 32 << 20
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
	if len(raw) > maxReplyBytes {
		raw = raw[:maxReplyBytes]
		readErr = fmt.Errorf("reply exceeds the %d-byte transport limit", maxReplyBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", Usage{}, fmt.Errorf("model server at %s returned %s: %s — is model %q loaded there?",
			c.baseURL, resp.Status, snippet(raw), c.model)
	}
	var decoded chatResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	decodeErr := dec.Decode(&decoded)
	if decodeErr == nil {
		var extra json.RawMessage
		if err := dec.Decode(&extra); err != io.EOF {
			decodeErr = err
			if decodeErr == nil {
				decodeErr = errors.New("unexpected extra JSON after the reply")
			}
		}
	}
	// On a read failure, a fully received JSON object can still preserve
	// partial text/identity/usage; it must never turn that failure into success.
	servedModel = decoded.Model
	if len(decoded.Choices) > 0 {
		reply = decoded.Choices[0].Message.Content
	}
	if decoded.Usage != nil {
		usage = Usage{In: decoded.Usage.Prompt, Out: decoded.Usage.Completion, Reported: true}
	}
	if readErr != nil {
		if errors.Is(readErr, context.Canceled) {
			return reply, servedModel, usage, fmt.Errorf("response cancelled: %w; reply is partial", readErr)
		}
		return reply, servedModel, usage, fmt.Errorf("cannot read reply from %s: %w — response is incomplete; check the server and retry (received: %s)", c.baseURL, readErr, snippet(raw))
	}
	if decodeErr != nil {
		return reply, servedModel, usage, fmt.Errorf("model server at %s returned bad JSON: %w — check the server response and retry", c.baseURL, decodeErr)
	}
	if msg, refused := errorMessage(decoded.Error); refused {
		return reply, servedModel, usage, fmt.Errorf("model server at %s refused the request: %s — check the server and retry", c.baseURL, msg)
	}
	if len(decoded.Choices) == 0 {
		return reply, servedModel, usage, fmt.Errorf("model server at %s returned no choices — check the server and retry", c.baseURL)
	}
	calls := decoded.Choices[0].Message.ToolCalls
	if capture != nil {
		capture.Message.ToolCalls = calls
		capture.FinishReason = decoded.Choices[0].FinishReason
	}
	if len(calls) > 0 && capture == nil {
		return reply, servedModel, usage, fmt.Errorf("unexpected tool calls in direct text reply")
	}
	if !(capture != nil && len(tools) > 0 && len(calls) > 0 && decoded.Choices[0].FinishReason == "tool_calls") {
		if err := c.finishError(decoded.Choices[0].FinishReason); err != nil {
			return reply, servedModel, usage, err
		}
	}
	if strings.TrimSpace(reply) == "" && len(calls) == 0 {
		return reply, servedModel, usage, fmt.Errorf("model server at %s returned an empty answer — check the server and retry", c.baseURL)
	}
	return reply, servedModel, usage, nil
}

// ChatStream works like Chat but calls onDelta for each streamed piece as it
// arrives, then returns the full assembled reply and served identity.
// On failure the returned text and usage preserve partial evidence; callers
// must not treat the nonnil error as a completed answer or execute its text.
func (c *Client) ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) (string, string, Usage, error) {
	return c.chatStream(ctx, msgs, onDelta, nil, nil)
}
func (c *Client) chatStream(ctx context.Context, msgs []Message, onDelta func(string), tools []ToolDefinition, capture *Reply) (reply, servedModel string, usage Usage, err error) {
	previous, continuing := ctx.Value(responseContinuationKey{}).(Reply)
	if continuing && capture != nil {
		capture.Message = previous.Message.Clone()
		reply, servedModel = previous.Message.Content, previous.Served
	}
	defer func() {
		if capture != nil {
			capture.Message.Content = reply
			capture.Served = servedModel
			capture.Usage = usage
		}
	}()
	if err := c.runtimeError(); err != nil {
		return reply, servedModel, Usage{}, err
	}
	if continuing && c.runtime != RuntimeMLX {
		return reply, servedModel, Usage{}, fmt.Errorf("selected runtime cannot continue an unfinished native response; explicit owned MLX token-prefix support is required")
	}
	if c.runtime == RuntimeOllama {
		return c.chatOllama(ctx, msgs, true, onDelta, tools, capture)
	}
	request := c.request(msgs, true)
	request.Tools = tools
	if continuing {
		if c.runtime != RuntimeMLX || previous.Prefix == nil {
			return reply, servedModel, Usage{}, fmt.Errorf("selected runtime cannot continue an unfinished native response; explicit owned MLX token-prefix support is required")
		}
		if err := previous.Prefix.Validate(); err != nil {
			return reply, servedModel, Usage{}, err
		}
		request.ResponsePrefix = previous.Prefix
	}
	body, err := json.Marshal(request)
	if err != nil {
		return reply, servedModel, Usage{}, fmt.Errorf("cannot encode request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return reply, servedModel, Usage{}, fmt.Errorf("cannot build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.do(req, capture)
	if err != nil {
		return reply, servedModel, Usage{}, c.plainConnErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return reply, servedModel, Usage{}, fmt.Errorf("model server at %s returned %s: %s — is model %q loaded there?",
			c.baseURL, resp.Status, snippet(raw), c.model)
	}
	var full, reasoning, reasoningContent strings.Builder
	if continuing {
		full.WriteString(previous.Message.Content)
		reasoning.WriteString(previous.Message.Reasoning)
		reasoningContent.WriteString(previous.Message.ReasoningContent)
	}
	// Callers get the same partial evidence as onDelta, including when a
	// later chunk or the connection fails. The nonnil error forbids success.
	defer func() { reply = full.String() }()
	defer func() {
		if capture != nil {
			capture.Message.Reasoning = reasoning.String()
			capture.Message.ReasoningContent = reasoningContent.String()
		}
	}()
	calls := map[int]ToolCall{}
	if continuing {
		for index, call := range previous.Message.ToolCalls {
			calls[index] = call
		}
	}
	defer func() {
		if capture != nil {
			capture.Message.ToolCalls, _ = orderedCalls(calls)
		}
	}()
	complete := false
	done := false
	var finishErr error
	var framingErr *ToolFramingError
	defer func() { err = errors.Join(finishErr, err) }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 32<<20) // Same transport guard; literal prefixes can span several output windows.
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // SSE comments, keepalives and event metadata carry no answer.
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			complete, done = true, true
			continue // Read through EOF: DONE cannot hide a broken HTTP transfer.
		}
		if framingErr != nil {
			return "", servedModel, usage, fmt.Errorf("model server sent data after failed tool boundary; response is partial")
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil || !strings.HasPrefix(data, "{") {
			if err == nil {
				err = errors.New("expected a JSON stream object")
			}
			return "", servedModel, usage, fmt.Errorf("model server at %s sent a bad stream chunk: %w — check the server and retry; reply is partial", c.baseURL, err)
		}
		if servedModel == "" {
			servedModel = chunk.Model
		}
		if continuing && previous.Served != "" && chunk.Model != "" && chunk.Model != previous.Served {
			return "", servedModel, usage, fmt.Errorf("unfinished response changed serving model; prefix refused")
		}
		if capture != nil && chunk.ResponsePrefix != nil {
			if capture.Prefix != nil || chunk.ResponsePrefix.Validate() != nil {
				return "", servedModel, usage, fmt.Errorf("invalid or repeated unfinished response prefix")
			}
			terminal := false
			for _, choice := range chunk.Choices {
				terminal = terminal || choice.FinishReason == "length"
			}
			if !terminal || continuing && !extendsPrefix(previous.Prefix, chunk.ResponsePrefix) {
				return "", servedModel, usage, fmt.Errorf("unfinished native response prefix is not the same response at a length boundary")
			}
			capture.Prefix = chunk.ResponsePrefix.Clone()
		}
		if chunk.Usage != nil {
			usage = Usage{In: chunk.Usage.Prompt, Out: chunk.Usage.Completion, Reported: true}
		}
		if msg, refused := errorMessage(chunk.Error); refused {
			if candidate := toolFramingError(chunk.Error); candidate != nil && capture != nil && len(tools) > 0 && !complete && !done && finishErr == nil && candidate.CallIndex == len(calls) {
				if _, callErr := orderedCalls(calls); callErr == nil {
					framingErr = candidate
					continue // Wait for clean EOF; a broken transfer is not repairable.
				}
			}
			return "", servedModel, usage, fmt.Errorf("model server at %s refused the stream: %s — check the server and retry; reply is partial", c.baseURL, msg)
		}
		if done {
			return "", servedModel, usage, fmt.Errorf("model server at %s sent data after [DONE] — check the server and retry; reply is partial", c.baseURL)
		}
		for _, ch := range chunk.Choices {
			if complete && (ch.Delta.Content != "" || ch.Delta.Reasoning != "" || ch.Delta.ReasoningContent != "" || len(ch.Delta.ToolCalls) > 0) {
				return "", servedModel, usage, fmt.Errorf("model server at %s sent answer content after stop — check the server and retry; reply is partial", c.baseURL)
			}
			if len(ch.Delta.ToolCalls) > 0 {
				if capture == nil || len(tools) == 0 {
					return "", servedModel, usage, fmt.Errorf("unexpected tool calls in text stream")
				}
				if e := assembleCalls(calls, ch.Delta.ToolCalls); e != nil {
					return "", servedModel, usage, e
				}
			}
			full.WriteString(ch.Delta.Content)
			reasoning.WriteString(ch.Delta.Reasoning)
			reasoningContent.WriteString(ch.Delta.ReasoningContent)
			size := full.Len() + reasoning.Len() + reasoningContent.Len()
			for _, call := range calls {
				size += len(call.Arguments) + len(call.Name) + len(call.ID)
			}
			if size > 32<<20 {
				return "", servedModel, usage, fmt.Errorf("reply exceeds transport limit")
			}
			if onDelta != nil && ch.Delta.Content != "" {
				onDelta(ch.Delta.Content)
			}
			if capture != nil && ch.FinishReason != "" {
				if capture.FinishReason != "" && capture.FinishReason != ch.FinishReason {
					return "", servedModel, usage, fmt.Errorf("stream changed its terminal finish reason; response is partial")
				}
				capture.FinishReason = ch.FinishReason
			}
			if !(capture != nil && len(calls) > 0 && ch.FinishReason == "tool_calls") {
				if e := c.finishError(ch.FinishReason); e != nil {
					if finishErr == nil {
						finishErr = e
					}
					complete = true // Keep reading usage; no later answer may be admitted.
				}
			}
			if ch.FinishReason == "stop" || ch.FinishReason == "tool_calls" {
				complete = true
			}
		}
		if finishErr == nil {
			for _, ch := range chunk.Choices {
				if err := observeAux(ctx, "reasoning", ch.Delta.Reasoning); err != nil {
					return "", servedModel, usage, err
				}
				if err := observeAux(ctx, "reasoning_content", ch.Delta.ReasoningContent); err != nil {
					return "", servedModel, usage, err
				}
				if err := observeAux(ctx, "content", ch.Delta.Content); err != nil {
					return "", servedModel, usage, err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return "", servedModel, usage, fmt.Errorf("response cancelled: %w; reply is partial", err)
		}
		return "", servedModel, usage, fmt.Errorf("stream from %s broke: %w — check the server and retry; reply is partial", c.baseURL, err)
	}
	if framingErr != nil {
		if cause := context.Cause(ctx); cause != nil {
			return "", servedModel, usage, cause
		}
		return "", servedModel, usage, framingErr
	}
	if finishErr != nil {
		if capture != nil && capture.FinishReason == "length" {
			capture.OutputLimit = true // Only a clean, fully received length terminal is resumable.
		}
		return "", servedModel, usage, nil // Deferred join retains the failed finish.
	}
	if !complete {
		return "", servedModel, usage, fmt.Errorf("stream from %s is incomplete: ended before stop or [DONE] — check the server and retry; reply is partial", c.baseURL)
	}
	if _, e := orderedCalls(calls); e != nil {
		return "", servedModel, usage, e
	}
	if strings.TrimSpace(full.String()) == "" && len(calls) == 0 {
		if capture == nil || capture.FinishReason != "stop" || (reasoning.Len() == 0 && reasoningContent.Len() == 0) {
			return "", servedModel, usage, fmt.Errorf("model server at %s completed an empty answer — check the server and retry", c.baseURL)
		}
	}
	return "", servedModel, usage, nil
}

// A length/filter/tool-call finish is not a completed text answer. Missing
// finish_reason is allowed for plain legacy replies and intermediate deltas;
// streams still require stop or DONE before success.
func (c *Client) finishError(reason string) error {
	if reason == "" || reason == "stop" {
		return nil
	}
	if reason == "length" {
		limit := "0/unset: runtime default (unknown)"
		if c.MaxOutputTokens != nil && *c.MaxOutputTokens > 0 {
			limit = fmt.Sprintf("%d including reasoning", *c.MaxOutputTokens)
		}
		return fmt.Errorf("model server at %s ended the reply with finish_reason %q — output is partial; params.max_output_tokens=%s; adjust the budget or requested work", c.baseURL, reason, limit)
	}
	return fmt.Errorf("model server at %s ended the reply with finish_reason %q — output is partial; check the server/generation limits and retry", c.baseURL, reason)
}

// OpenAI-compatible backends use either an error object or an error string.
// Any non-null error envelope refuses, even if it omits a useful message.
func errorMessage(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var api apiError
	if json.Unmarshal(raw, &api) == nil && api.Message != "" {
		return api.Message, true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return text, true
	}
	return snippet(raw), true
}

func (c *Client) plainConnErr(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("request cancelled: %w", err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return fmt.Errorf("cannot reach model server at %s — is it running? (tried %s): %w", c.baseURL, opErr.Addr, err)
	}
	return fmt.Errorf("request to model server at %s failed: %w", c.baseURL, err)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}

// WithHTTPClient supplies the caller-owned transport policy without changing it.
func (c *Client) WithHTTPClient(client *http.Client) *Client { c.http = client; return c }
