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
	criteria := make(map[string]string, len(r.Options))
	for i, option := range r.Options {
		criteria[strconv.Itoa(i)] = option
	}
	body, err := json.Marshal(map[string]any{
		"model": c.model, "state": r.Context,
		"questions": map[string]any{"decision": map[string]any{
			"type": "choice", "instructions": r.Instructions, "criteria": criteria,
		}},
		"keep_alive": "60m",
	})
	if err != nil {
		return Verdict{}, err
	}
	if r.MaxBytes < 0 {
		return Verdict{}, fmt.Errorf("judge request byte limit must not be negative")
	}
	if r.MaxBytes > 0 && len(body) > r.MaxBytes {
		return Verdict{}, &ContextCapacityError{Message: fmt.Sprintf("judge complete request bytes %d exceed limit %d", len(body), r.MaxBytes), EncodedRequest: true}
	}
	// Ollama's local System One contract caps text/schema at 64 KiB. Counting
	// the complete body is conservative; actual token admission stays with Ollama.
	if len(body) > 64<<10 {
		return Verdict{}, &ContextCapacityError{Message: "Ollama System One request exceeds 64 KiB", EncodedRequest: true}
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
	return Verdict{Pick: pick, Option: r.Options[pick], Confidence: confidence, Model: result.Model, Device: "unknown", Protocol: "ollama_system_one", ConfidenceSource: "native_probability"}, nil
}
