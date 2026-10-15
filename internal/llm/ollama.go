package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ollamaRequest struct {
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Stream   bool             `json:"stream"`
	Think    *bool            `json:"think,omitempty"`
	Options  map[string]any   `json:"options,omitempty"`
	Truncate bool             `json:"truncate"`
	Shift    bool             `json:"shift"`
}
type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content  string          `json:"content"`
		Thinking string          `json:"thinking"`
		Calls    json.RawMessage `json:"tool_calls"`
	} `json:"message"`
	Done       bool            `json:"done"`
	DoneReason string          `json:"done_reason"`
	In         *int            `json:"prompt_eval_count"`
	Out        *int            `json:"eval_count"`
	Error      json.RawMessage `json:"error"`
}

func (c *Client) ollamaRoot() string { return strings.TrimSuffix(c.baseURL, "/v1") }

// chatOllama is the native plain/NDJSON text branch. Thinking never enters the
// answer, callback or tool parser. Every completed answer needs a done event.
func (c *Client) chatOllama(ctx context.Context, msgs []Message, stream bool, onDelta func(string), tools []ToolDefinition, capture *Reply) (reply, served string, usage Usage, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("native model server at %s: %w", c.baseURL, err)
		}
	}()
	r := ollamaRequest{Model: c.model, Messages: nativeMessages(msgs), Tools: tools, Stream: stream, Think: c.ollamaThinking(ctx), Options: map[string]any{}}
	if len(tools) > 0 {
		c.thinkingMu.Lock()
		unsupported := c.thinkingMeta != nil && c.thinkingMeta.tools != nil && !*c.thinkingMeta.tools
		c.thinkingMu.Unlock()
		if unsupported {
			return "", "", Usage{}, fmt.Errorf("selected Ollama model does not advertise tools; select model.tool_protocol=text explicitly or a compatible model")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", "", Usage{}, err
	}
	if c.Temperature != nil {
		r.Options["temperature"] = *c.Temperature
	}
	if c.TopK != nil {
		r.Options["top_k"] = *c.TopK
	}
	if c.RepetitionPenalty != nil {
		r.Options["repeat_penalty"] = *c.RepetitionPenalty
	}
	if c.MaxOutputTokens != nil && *c.MaxOutputTokens > 0 {
		r.Options["num_predict"] = *c.MaxOutputTokens
	}
	body, err := json.Marshal(r)
	if err != nil {
		return "", "", Usage{}, fmt.Errorf("cannot encode native request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ollamaRoot()+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", "", Usage{}, fmt.Errorf("cannot build native request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "application/x-ndjson")
	}
	resp, err := c.do(req, capture)
	if err != nil {
		return "", "", Usage{}, c.plainConnErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		refusal := fmt.Errorf("returned %s: %s — is model %q loaded there?", resp.Status, snippet(raw), c.model)
		if readErr != nil {
			refusal = fmt.Errorf("%w; cannot read error response: %w", refusal, readErr)
		}
		return "", "", Usage{}, refusal
	}
	var full, thinking strings.Builder
	defer func() { reply = full.String() }()
	defer func() {
		if capture != nil {
			capture.Message.Thinking = thinking.String()
		}
	}()
	done := false
	consume := func(chunk ollamaResponse) error {
		if done {
			if msg, refused := errorMessage(chunk.Error); refused {
				return fmt.Errorf("native server refused after done: %s; reply is partial", msg)
			}
			return fmt.Errorf("native server sent data after done; reply is partial")
		}
		if served == "" {
			served = chunk.Model
		}
		if chunk.In != nil {
			usage.In = *chunk.In
		}
		if chunk.Out != nil {
			usage.Out = *chunk.Out
		}
		if chunk.In != nil && chunk.Out != nil {
			usage.Reported = true
		}
		calls, e := nativeCalls(chunk.Message.Calls)
		if e != nil {
			return e
		}
		if len(calls) > 0 {
			if capture == nil || len(tools) == 0 {
				return fmt.Errorf("unexpected tool calls in native text reply")
			}
			capture.Message.ToolCalls = append(capture.Message.ToolCalls, calls...)
		}
		full.WriteString(chunk.Message.Content)
		thinking.WriteString(chunk.Message.Thinking)
		size := full.Len() + thinking.Len()
		if capture != nil {
			size += capture.Message.Size()
		}
		if size > 32<<20 {
			return fmt.Errorf("native reply exceeds transport limit")
		}
		if onDelta != nil && chunk.Message.Content != "" {
			onDelta(chunk.Message.Content)
		}
		if msg, refused := errorMessage(chunk.Error); refused {
			return fmt.Errorf("native model server refused the request: %s; reply is partial", msg)
		}
		if chunk.Done {
			done = true
			if capture != nil {
				capture.FinishReason = chunk.DoneReason
			}
			if err := c.finishError(chunk.DoneReason); err != nil {
				return err
			}
		}
		if stream {
			if err := observeAux(ctx, "thinking", chunk.Message.Thinking); err != nil {
				return err
			}
			return observeAux(ctx, "content", chunk.Message.Content)
		}
		return nil
	}
	if stream {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1024), 1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var chunk ollamaResponse
			if err := json.Unmarshal(line, &chunk); err != nil || line[0] != '{' {
				if err == nil {
					err = fmt.Errorf("expected a JSON stream object")
				}
				return "", served, usage, fmt.Errorf("native server sent bad NDJSON: %w; reply is partial", err)
			}
			if err := consume(chunk); err != nil {
				return "", served, usage, err
			}
		}
		if err := sc.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return "", served, usage, fmt.Errorf("response cancelled: %w; reply is partial", err)
			}
			return "", served, usage, fmt.Errorf("native stream broke: %w; reply is partial", err)
		}
	} else {
		const maxReplyBytes = 32 << 20
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
		if len(raw) > maxReplyBytes {
			raw = raw[:maxReplyBytes]
			readErr = fmt.Errorf("native reply exceeds %d-byte transport limit", maxReplyBytes)
		}
		var chunk ollamaResponse
		decodeErr := decodeSingle(raw, &chunk)
		consumeErr := consume(chunk)
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) {
				return "", served, usage, fmt.Errorf("response cancelled: %w; reply is partial", readErr)
			}
			return "", served, usage, fmt.Errorf("cannot read native reply: %w; reply is incomplete", readErr)
		}
		if decodeErr != nil {
			return "", served, usage, fmt.Errorf("native server returned bad JSON: %w; reply is incomplete", decodeErr)
		}
		if consumeErr != nil {
			return "", served, usage, consumeErr
		}
	}
	if !done {
		return "", served, usage, fmt.Errorf("native reply ended before done; reply is incomplete")
	}
	if capture != nil {
		if err := assignNativeIDs(capture.Message.ToolCalls); err != nil {
			return "", served, usage, err
		}
	}
	if strings.TrimSpace(full.String()) == "" && (capture == nil || len(capture.Message.ToolCalls) == 0) {
		if capture == nil || capture.FinishReason != "stop" || thinking.Len() == 0 {
			return "", served, usage, fmt.Errorf("native server completed an empty answer; check model/server")
		}
	}
	return "", served, usage, nil
}
