package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

// judgeChat adapts an explicitly selected chat endpoint. Confidence is the
// model's own estimate; the endpoint does not establish device or residency.
func (c *Client) judgeChat(ctx context.Context, r Request) (Verdict, error) {
	payload, err := json.Marshal(struct {
		Context      string   `json:"context"`
		Instructions string   `json:"instructions"`
		Options      []string `json:"options"`
	}{r.Context, r.Instructions, r.Options})
	if err != nil {
		return Verdict{}, err
	}
	request := map[string]any{
		"model": c.model, "stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": c.Prompts.Text("decision.chat.instructions")},
			{"role": "user", "content": string(payload)},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	if c.outputTokens > 0 {
		request["max_tokens"] = c.outputTokens
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Verdict{}, err
	}
	if r.MaxBytes < 0 || r.MaxBytes > 0 && len(body) > r.MaxBytes {
		return Verdict{}, fmt.Errorf("judge complete request bytes %d exceed limit %d", len(body), r.MaxBytes)
	}
	base := strings.TrimSuffix(c.baseURL, "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("chat decision request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 512))
		if err != nil {
			return Verdict{}, fmt.Errorf("chat decision HTTP %d: %w", resp.StatusCode, err)
		}
		return Verdict{}, fmt.Errorf("chat decision HTTP %d: %s", resp.StatusCode, excerpt(b))
	}
	// Match the existing chat transport guard (internal/llm/client.go); reasoning
	// belongs to the envelope, not the much smaller typed verdict.
	b, err := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	if err != nil {
		return Verdict{}, err
	}
	if len(b) > 32<<20 {
		return Verdict{}, fmt.Errorf("chat decision response exceeds 32MiB transport guard")
	}
	if err := uniqueJSON(b); err != nil {
		return Verdict{}, fmt.Errorf("chat decision response: %w", err)
	}
	var envelope struct {
		Model   string          `json:"model"`
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Role         string          `json:"role"`
				Content      *string         `json:"content"`
				ToolCalls    json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage `json:"function_call"`
				Refusal      json.RawMessage `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return Verdict{}, err
	}
	if envelope.Model != c.model {
		return Verdict{}, fmt.Errorf("chat decision model %q mismatches %q", envelope.Model, c.model)
	}
	if presentJSON(envelope.Error) || len(envelope.Choices) != 1 {
		return Verdict{}, fmt.Errorf("chat decision requires one error-free choice")
	}
	choice := envelope.Choices[0]
	if choice.Finish != "stop" || choice.Message.Role != "assistant" || choice.Message.Content == nil || presentJSON(choice.Message.ToolCalls) || presentJSON(choice.Message.FunctionCall) || presentJSON(choice.Message.Refusal) {
		return Verdict{}, fmt.Errorf("chat decision requires a complete assistant answer without tool calls or refusal (finish_reason=%q)", choice.Finish)
	}
	content := []byte(*choice.Message.Content)
	if err := uniqueJSON(content); err != nil {
		return Verdict{}, fmt.Errorf("chat decision verdict: %w", err)
	}
	var result struct {
		Pick       *int     `json:"pick"`
		Option     *string  `json:"option"`
		Confidence *float64 `json:"confidence"`
	}
	if err := decode(bytes.NewReader(content), &result); err != nil {
		return Verdict{}, err
	}
	if result.Pick == nil || result.Option == nil || result.Confidence == nil {
		return Verdict{}, fmt.Errorf("chat decision needs pick, option and confidence")
	}
	if *result.Pick < 0 || *result.Pick >= len(r.Options) || *result.Option != r.Options[*result.Pick] {
		return Verdict{}, fmt.Errorf("chat decision pick/option mismatch")
	}
	confidence := *result.Confidence
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return Verdict{}, fmt.Errorf("chat decision confidence is not a finite probability")
	}
	return Verdict{Pick: *result.Pick, Option: *result.Option, Confidence: confidence, Model: envelope.Model, Device: "unknown", Protocol: "openai-chat", ConfidenceSource: "model-self-report"}, nil
}

func presentJSON(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s != "" && s != "null" && s != "[]"
}

// Reject duplicate keys before decoding into Go structs, which otherwise keep
// the last value. This includes duplicates in the envelope and verdict alike.
func uniqueJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		seen := map[string]bool{}
		for d.More() {
			if delimiter == '{' {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				name = strings.ToLower(name)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON key %v", key)
				}
				seen[name] = true
			}
			if err := value(); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
