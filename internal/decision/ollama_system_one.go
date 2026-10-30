package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// D1 is a decision model, not a chat completion model. Explicit protocol
// selection keeps existing Ollama chat judges backward compatible.
func (c *Client) judgeOllamaSystemOne(ctx context.Context, r Request) (Verdict, error) {
	body, err := c.systemOneRequest(r)
	if err != nil {
		return Verdict{}, err
	}
	criteria := make(map[string]string, len(r.Options))
	for i, option := range r.Options {
		criteria[strconv.Itoa(i)] = option
	}
	endpoint := strings.TrimSuffix(c.baseURL, "/v1") + "/v1/systemone"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("Ollama System One: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, err := io.ReadAll(io.LimitReader(response.Body, 512))
		if err != nil {
			return Verdict{}, err
		}
		if response.StatusCode == http.StatusRequestEntityTooLarge {
			return Verdict{}, &ContextCapacityError{Message: "Ollama System One provider limit: " + excerpt(raw), EncodedRequest: true}
		}
		if response.StatusCode == http.StatusBadRequest && nativeCapacityRefusal(raw) {
			return Verdict{}, &ContextCapacityError{Message: "Ollama System One: " + excerpt(raw)}
		}
		return Verdict{}, fmt.Errorf("Ollama System One HTTP %d: %s", response.StatusCode, excerpt(raw))
	}
	var result struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type          string              `json:"type"`
			Choice        string              `json:"choice"`
			Probabilities map[string]*float64 `json:"probabilities"`
			Confidence    *float64            `json:"confidence"`
		} `json:"answers"`
		Usage json.RawMessage `json:"usage"`
	}
	if err = decode(response.Body, &result); err != nil {
		return Verdict{}, fmt.Errorf("Ollama System One: %w", err)
	}
	if result.Model != c.model {
		return Verdict{}, fmt.Errorf("Ollama decision model %q mismatches %q", result.Model, c.model)
	}
	answer, ok := result.Answers["decision"]
	if !ok || len(result.Answers) != 1 || answer.Type != "choice" {
		return Verdict{}, fmt.Errorf("Ollama decision needs exactly the requested choice answer")
	}
	pick, err := strconv.Atoi(answer.Choice)
	if err != nil || pick < 0 || pick >= len(r.Options) || strconv.Itoa(pick) != answer.Choice {
		return Verdict{}, fmt.Errorf("Ollama decision selected an unknown option")
	}
	if len(answer.Probabilities) != len(r.Options) {
		return Verdict{}, fmt.Errorf("Ollama decision probabilities do not cover the supplied options")
	}
	sum := 0.0
	for key, value := range answer.Probabilities {
		if value == nil {
			return Verdict{}, fmt.Errorf("Ollama decision has a null probability")
		}
		p := *value
		if _, ok := criteria[key]; !ok || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return Verdict{}, fmt.Errorf("Ollama decision has an invalid probability")
		}
		sum += p
	}
	if math.Abs(sum-1) > .01 {
		return Verdict{}, fmt.Errorf("Ollama decision probabilities do not sum to one")
	}
	confidence := *answer.Probabilities[answer.Choice]
	for _, p := range answer.Probabilities {
		if *p > confidence+.000001 {
			return Verdict{}, fmt.Errorf("Ollama decision choice is not its highest-probability option")
		}
	}
	// Provider confidence is a concentration score, not P(chosen option).
	// Caliper's admission thresholds use the normalized chosen probability.
	return Verdict{Pick: pick, Option: r.Options[pick], Confidence: confidence, Model: result.Model, Device: "unknown", Protocol: "ollama_system_one", ConfidenceSource: "native_probability", Usage: optionalUsage(result.Usage)}, nil
}

// systemOneRequest is shared by dry admission and submission: escaping and
// envelope overhead are counted identically, without a network request.
func (c *Client) systemOneRequest(r Request) ([]byte, error) {
	criteria := make(map[string]string, len(r.Options))
	for i, option := range r.Options {
		criteria[strconv.Itoa(i)] = option
	}
	body, err := json.Marshal(map[string]any{
		"model": c.model, "state": r.Context,
		"questions": map[string]any{"decision": map[string]any{
			"type": "choice", "instructions": r.Instructions, "criteria": criteria,
		}}, "keep_alive": "60m",
	})
	if err != nil {
		return nil, err
	}
	// The provider endpoint itself refuses text+schema over 64 KiB
	// (HTTP 413, verified against the live endpoint). Predict that exact
	// refusal locally so callers can split the request instead of failing.
	const limit = 64 << 10
	if len(body) > limit {
		return nil, &ContextCapacityError{Message: fmt.Sprintf("Ollama System One provider limit: text and schema must not exceed 64 KiB (complete request %d bytes)", len(body)), EncodedRequest: true}
	}
	return body, nil
}
